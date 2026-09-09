package cwlexec

import (
	"testing"

	"github.com/yardrail/cwl-go/pkg/cwlcore"
	"github.com/yardrail/cwl-go/pkg/salad"
)

func TestValidateInputs(t *testing.T) {
	t.Parallel()

	proc := func(params ...cwlcore.WorkflowInputParameter) cwlcore.Process {
		return &cwlcore.ExpressionTool{Inputs: params}
	}

	param := func(name string, typ cwlcore.TypeRef, def salad.Node) cwlcore.WorkflowInputParameter {
		return cwlcore.WorkflowInputParameter{
			ParameterBase: cwlcore.ParameterBase{IDField: name, Type: typ},
			Default:       def,
		}
	}

	stringDefault := salad.NewStringNode(salad.SourceLine{}, "world")

	cases := []struct {
		name    string
		process cwlcore.Process
		inputs  map[string]any
		opts    []ValidateOption
		want    map[string]any
		wantErr error
	}{
		{
			name:    "valid inputs",
			process: proc(param("message", primitive(cwlcore.PrimitiveString), nil)),
			inputs:  object("message", "hello"),
			want:    object("message", "hello"),
		},
		{
			name:    "missing required input",
			process: proc(param("message", primitive(cwlcore.PrimitiveString), nil)),
			inputs:  make(map[string]any),
			wantErr: ErrInputRequired,
		},
		{
			name:    "wrong type",
			process: proc(param("message", primitive(cwlcore.PrimitiveString), nil)),
			inputs:  object("message", 42),
			wantErr: ErrOutputType,
		},
		{
			name:    "unknown input rejected",
			process: proc(param("message", primitive(cwlcore.PrimitiveString), nil)),
			inputs:  object("message", "hi", "extra", 1),
			opts:    []ValidateOption{WithRejectUnknown()},
			wantErr: ErrInputUnknown,
		},
		{
			name:    "unknown input accepted by default",
			process: proc(param("message", primitive(cwlcore.PrimitiveString), nil)),
			inputs:  object("message", "hi", "extra", 1),
			want:    object("message", "hi"),
		},
		{
			name:    "optional input omitted",
			process: proc(param("message", optional(primitive(cwlcore.PrimitiveString)), nil)),
			inputs:  make(map[string]any),
			want:    object("message", nil),
		},
		{
			name:    "default fills in",
			process: proc(param("message", primitive(cwlcore.PrimitiveString), stringDefault)),
			inputs:  make(map[string]any),
			want:    object("message", "world"),
		},
		{
			name: "multiple errors collected",
			process: proc(
				param("a", primitive(cwlcore.PrimitiveString), nil),
				param("b", primitive(cwlcore.PrimitiveInt), nil),
			),
			inputs:  make(map[string]any),
			wantErr: ErrInputRequired,
		},
		{
			name:    "nil inputs map with default",
			process: proc(param("message", primitive(cwlcore.PrimitiveString), stringDefault)),
			inputs:  nil,
			want:    object("message", "world"),
		},
		{
			name:    "nil value treated as absent with default",
			process: proc(param("message", primitive(cwlcore.PrimitiveString), stringDefault)),
			inputs:  object("message", nil),
			want:    object("message", "world"),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			merged, err := ValidateInputs(tc.process, tc.inputs, tc.opts...)

			if tc.wantErr != nil {
				assertErrorIs(t, "ValidateInputs", err, tc.wantErr)

				return
			}

			if err != nil {
				t.Fatalf("ValidateInputs: unexpected error: %v", err)
			}

			assertDeepEqual(t, "merged", merged, tc.want)
		})
	}
}

func TestValidateInputsMultipleErrorsMentionsBothInputs(t *testing.T) {
	t.Parallel()

	process := &cwlcore.ExpressionTool{Inputs: []cwlcore.WorkflowInputParameter{
		{ParameterBase: cwlcore.ParameterBase{IDField: "a", Type: primitive(cwlcore.PrimitiveString)}},
		{ParameterBase: cwlcore.ParameterBase{IDField: "b", Type: primitive(cwlcore.PrimitiveInt)}},
	}}

	_, err := ValidateInputs(process, make(map[string]any))
	if err == nil {
		t.Fatal("expected error")
	}

	msg := err.Error()
	if !containsAll(msg, `"a"`, `"b"`) {
		t.Fatalf("error should name both inputs, got: %s", msg)
	}
}
