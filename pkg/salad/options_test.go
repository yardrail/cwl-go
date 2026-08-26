package salad

import "testing"

// stubFetcher is a Fetcher that serves nothing; it exists so the option
// constructors can be exercised without a real fetcher.
type stubFetcher struct{}

var _ Fetcher = stubFetcher{}

func (stubFetcher) FetchText(string) ([]byte, error)        { return nil, nil }
func (stubFetcher) Exists(string) bool                      { return false }
func (stubFetcher) Normalize(_, ref string) (string, error) { return ref, nil }

func TestLoaderOptions(t *testing.T) {
	t.Parallel()

	fetcher := stubFetcher{}

	var cfg loaderConfig
	for _, opt := range []LoaderOption{
		WithFetcher(fetcher),
		WithBaseURL("file:///workspace/"),
		WithSkipLinkCheck(true),
	} {
		opt(&cfg)
	}

	if cfg.fetcher != Fetcher(fetcher) {
		t.Error("WithFetcher did not set the fetcher")
	}

	if cfg.baseURL != "file:///workspace/" {
		t.Errorf("baseURL = %q, want the configured value", cfg.baseURL)
	}

	if !cfg.skipLinkCheck {
		t.Error("WithSkipLinkCheck did not set the flag")
	}
}

func TestNewLoaderAppliesOptions(t *testing.T) {
	t.Parallel()

	l := NewLoader(WithBaseURL("file:///a/"), WithSkipLinkCheck(true))
	if l.cfg.baseURL != "file:///a/" || !l.cfg.skipLinkCheck {
		t.Errorf("NewLoader did not apply its options: %+v", l.cfg)
	}

	if NewLoader().cfg.fetcher != nil {
		t.Error("a loader with no options should have no fetcher configured")
	}
}

func TestValidateOptions(t *testing.T) {
	t.Parallel()

	var cfg validateConfig
	if cfg.strict || cfg.strictForeign {
		t.Fatal("the zero validateConfig should be permissive")
	}

	Strict(true)(&cfg)
	StrictForeign(true)(&cfg)

	if !cfg.strict || !cfg.strictForeign {
		t.Errorf("options did not apply: %+v", cfg)
	}

	Strict(false)(&cfg)

	if cfg.strict {
		t.Error("Strict(false) did not clear the flag")
	}
}
