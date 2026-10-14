package cwlexec

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// scatterOverInput is a single step scattering over the workflow's array input.
func scatterOverInput() *wfSpec {
	return &wfSpec{
		inputs: []string{"xs"},
		steps: []stepSpec{{
			name:    stepFan,
			in:      []inSpec{{name: portIn, sources: []string{"xs"}}},
			out:     []string{portA},
			scatter: []string{portIn},
		}},
		outputs: []outSpec{{name: portFinal, sources: []string{"fan/" + portA}}},
	}
}

// scatterWidth is how many sub-jobs the concurrency fixtures expand into.
const scatterWidth = 8

// elements is the array of distinct values those sub-jobs scatter over.
func elements() []any {
	values := make([]any, 0, scatterWidth)
	for index := range scatterWidth {
		values = append(values, index)
	}

	return values
}

// concurrencyProbe records how many handler calls were in flight at once.
type concurrencyProbe struct {
	mu      sync.Mutex
	current int
	peak    int
}

// enter records the start of a call and returns the value the call should publish.
func (p *concurrencyProbe) enter() {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.current++
	p.peak = max(p.peak, p.current)
}

// leave records the end of a call.
func (p *concurrencyProbe) leave() {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.current--
}

// observed reports the highest number of calls seen in flight at once.
func (p *concurrencyProbe) observed() int {
	p.mu.Lock()
	defer p.mu.Unlock()

	return p.peak
}

// probingHandler answers a call after holding it open long enough for the probe to observe overlap.
func probingHandler(probe *concurrencyProbe) func(context.Context, *StepCall) (Result, error) {
	return func(_ context.Context, call *StepCall) (Result, error) {
		probe.enter()
		defer probe.leave()

		time.Sleep(20 * time.Millisecond)

		return Success(object(portA, call.Inputs[portIn]))
	}
}

func TestMaxParallelBoundsConcurrency(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		cap  int
		want int
	}{
		{name: "serialized", cap: 1, want: 1},
		{name: "capped", cap: 3, want: 3},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			probe := &concurrencyProbe{}
			runner := mustRunner(
				t,
				scatterOverInput(),
				testRegistry(probingHandler(probe)),
				&Config{MaxParallel: tc.cap},
			)

			result := mustRun(t, runner, object("xs", elements()))
			assertDeepEqual(t, "gathered", result.Outputs[portFinal], elements())

			if peak := probe.observed(); peak > tc.want {
				t.Fatalf("observed peak concurrency %d, want at most %d", peak, tc.want)
			}
		})
	}
}

// barrierHandler blocks every call until all width of them have arrived, so that a run which cannot
// reach that width deadlocks rather than passing by luck.
func barrierHandler(width int) func(context.Context, *StepCall) (Result, error) {
	var arrived atomic.Int64

	open := make(chan struct{})

	return func(_ context.Context, call *StepCall) (Result, error) {
		if arrived.Add(1) == int64(width) {
			close(open)
		}

		<-open

		return Success(object(portA, call.Inputs[portIn]))
	}
}

func TestUnboundedParallelismRunsEveryScatterJobAtOnce(t *testing.T) {
	t.Parallel()

	runner := mustRunner(t, scatterOverInput(), testRegistry(barrierHandler(scatterWidth)), nil)

	result := mustRun(t, runner, object("xs", elements()))
	assertDeepEqual(t, "gathered", result.Outputs[portFinal], elements())
}

func TestRunReturnsContextErrorWithUsableState(t *testing.T) {
	t.Parallel()

	started := make(chan struct{})

	registry := testRegistry(func(ctx context.Context, _ *StepCall) (Result, error) {
		close(started)
		<-ctx.Done()

		return PermanentFail(ctx.Err())
	})

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	go func() {
		<-started
		cancel()
	}()

	result, err := mustRunner(t, linearWorkflow(), registry, nil).Run(ctx, object("x", "seed"))
	assertErrorIs(t, "Run", err, context.Canceled)

	encoded, marshalErr := result.State.MarshalJSON()
	if marshalErr != nil {
		t.Fatalf("MarshalJSON: unexpected error: %v", marshalErr)
	}

	var restored RunState

	unmarshalErr := restored.UnmarshalJSON(encoded)
	if unmarshalErr != nil {
		t.Fatalf("UnmarshalJSON: unexpected error: %v", unmarshalErr)
	}

	if restored.version != RunStateVersion {
		t.Fatalf("restored version = %d, want %d", restored.version, RunStateVersion)
	}

	if _, recorded := restored.steps["s1"]; !recorded {
		t.Fatalf("state records steps %v, want an entry for s1", restored.steps)
	}
}

// identityHandler echoes each input value as the output, preserving the original type.
func identityHandler() func(context.Context, *StepCall) (Result, error) {
	return func(_ context.Context, call *StepCall) (Result, error) {
		return Success(object(portA, call.Inputs[portIn]))
	}
}

type invocationResult struct {
	id     int
	result Result
	err    error
}

func serialExecutor() func(*DispatchLoop) error {
	return func(loop *DispatchLoop) error {
		for {
			pending := loop.Pending()
			if len(pending) == 0 && loop.InFlight() == 0 {
				return nil
			}

			for _, inv := range pending {
				result, err := inv.Run(context.Background())
				loop.Complete(inv.ID(), result, err)
			}
		}
	}
}

func TestExecuteFuncRoutesStepExecution(t *testing.T) {
	t.Parallel()

	var executed atomic.Int64

	cfg := &Config{
		ExecuteFunc: func(loop *DispatchLoop) error {
			for {
				pending := loop.Pending()
				if len(pending) == 0 && loop.InFlight() == 0 {
					return nil
				}

				for _, inv := range pending {
					executed.Add(1)

					result, err := inv.Run(context.Background())
					loop.Complete(inv.ID(), result, err)
				}
			}
		},
	}

	runner := mustRunner(t, scatterOverInput(), testRegistry(identityHandler()), cfg)
	result := mustRun(t, runner, object("xs", elements()))

	assertDeepEqual(t, "gathered", result.Outputs[portFinal], elements())

	if got := executed.Load(); got != int64(scatterWidth) {
		t.Fatalf("executed %d invocations, want %d", got, scatterWidth)
	}
}

func goroutineExecutor(probe *concurrencyProbe) func(*DispatchLoop) error {
	return func(loop *DispatchLoop) error {
		done := make(chan invocationResult)
		dispatch := func(inv StepInvocation) {
			go func() {
				probe.enter()

				time.Sleep(20 * time.Millisecond)

				result, err := inv.Run(context.Background())

				probe.leave()

				done <- invocationResult{inv.ID(), result, err}
			}()
		}

		for {
			for _, inv := range loop.Pending() {
				dispatch(inv)
			}

			if loop.InFlight() == 0 {
				return nil
			}

			c := <-done
			loop.Complete(c.id, c.result, c.err)
		}
	}
}

func TestExecuteFuncWithGoroutines(t *testing.T) {
	t.Parallel()

	probe := &concurrencyProbe{}
	cfg := &Config{ExecuteFunc: goroutineExecutor(probe)}

	runner := mustRunner(t, scatterOverInput(), testRegistry(identityHandler()), cfg)
	result := mustRun(t, runner, object("xs", elements()))

	assertDeepEqual(t, "gathered", result.Outputs[portFinal], elements())

	if peak := probe.observed(); peak < 2 {
		t.Fatalf("observed peak concurrency %d, want >1", peak)
	}
}

func TestExecuteFuncNilUsesDefault(t *testing.T) {
	t.Parallel()

	runner := mustRunner(t, scatterOverInput(), testRegistry(identityHandler()), nil)
	result := mustRun(t, runner, object("xs", elements()))

	assertDeepEqual(t, "gathered", result.Outputs[portFinal], elements())
}

func TestExecuteFuncRespectsMaxParallel(t *testing.T) {
	t.Parallel()

	probe := &concurrencyProbe{}

	cfg := &Config{
		MaxParallel: 2,
		ExecuteFunc: goroutineExecutor(probe),
	}

	runner := mustRunner(t, scatterOverInput(), testRegistry(identityHandler()), cfg)
	result := mustRun(t, runner, object("xs", elements()))

	assertDeepEqual(t, "gathered", result.Outputs[portFinal], elements())

	if peak := probe.observed(); peak > 2 {
		t.Fatalf("observed peak concurrency %d, want at most 2", peak)
	}
}

func TestExecuteFuncHandlerError(t *testing.T) {
	t.Parallel()

	registry := testRegistry(func(_ context.Context, _ *StepCall) (Result, error) {
		return PermanentFail(errBoom)
	})

	cfg := &Config{ExecuteFunc: serialExecutor()}

	runner := mustRunner(t, linearWorkflow(), registry, cfg)

	result, err := runner.Run(t.Context(), object("x", "seed"))
	if err == nil {
		t.Fatal("expected error from handler")
	}

	if result.Status != StatusPermanentFail {
		t.Fatalf("status = %q, want %q", result.Status, StatusPermanentFail)
	}
}
