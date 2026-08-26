package cwlcore

import (
	"fmt"
	"testing"
)

// Expression sources reused across the union-wrapper tests.
const (
	exprReuse = "$(inputs.reuse)"
	exprCount = "$(inputs.n)"
	exprFlag  = "$(inputs.flag)"

	// direntName is a stand-in staged-file name.
	direntName = "out.txt"

	// The two boolean renderings OptBool.String produces.
	boolTrue  = "true"
	boolFalse = "false"
)

// assertKind fails unless the given ValueKind matches.
func assertKind(t *testing.T, got, want ValueKind) {
	t.Helper()

	if got != want {
		t.Errorf("Kind() = %v, want %v", got, want)
	}
}

// assertString fails unless a wrapper's String rendering matches.
func assertString(t *testing.T, got, want string) {
	t.Helper()

	if got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
}

func TestOptBool(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		value   OptBool
		wantOr  bool
		wantSet bool
		wantVal bool
	}{
		{name: "zero value is unset", value: OptBool{}, wantSet: false, wantVal: false, wantOr: true},
		{name: "explicit true", value: NewOptBool(true), wantSet: true, wantVal: true, wantOr: true},
		{name: "explicit false", value: NewOptBool(false), wantSet: true, wantVal: false, wantOr: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := tc.value.IsSet(); got != tc.wantSet {
				t.Errorf("IsSet() = %v, want %v", got, tc.wantSet)
			}

			if got := tc.value.Bool(); got != tc.wantVal {
				t.Errorf("Bool() = %v, want %v", got, tc.wantVal)
			}

			// Or(true) is how the schema default for separate and
			// shellQuote is applied. An explicit false must survive it.
			if got := tc.value.Or(true); got != tc.wantOr {
				t.Errorf("Or(true) = %v, want %v", got, tc.wantOr)
			}
		})
	}
}

func TestOptBoolString(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		want  string
		value OptBool
	}{
		{name: "unset", value: OptBool{}, want: kindNameUnset},
		{name: boolTrue, value: NewOptBool(true), want: boolTrue},
		{name: boolFalse, value: NewOptBool(false), want: boolFalse},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assertString(t, tc.value.String(), tc.want)
		})
	}
}

func TestExprBoolZeroValue(t *testing.T) {
	t.Parallel()

	zero := ExprBool{}
	assertKind(t, zero.Kind(), ValueUnset)

	if zero.IsSet() {
		t.Error("IsSet() = true on the zero ExprBool")
	}

	if zero.Bool() || zero.Expression() != "" {
		t.Error("zero ExprBool returned a non-zero payload")
	}
}

func TestExprBoolLiteral(t *testing.T) {
	t.Parallel()

	literal := NewExprBool(true)
	assertKind(t, literal.Kind(), ValueBool)

	if !literal.IsSet() || !literal.Bool() {
		t.Errorf("NewExprBool(true) = %+v, want a set boolean literal", literal)
	}

	// Reading the wrong member returns that member's zero value rather than
	// panicking; Kind is the discriminator.
	if literal.Expression() != "" {
		t.Errorf("Expression() on a boolean literal = %q, want empty", literal.Expression())
	}

	assertString(t, literal.String(), boolTrue)
}

func TestExprBoolExpression(t *testing.T) {
	t.Parallel()

	expr := NewExprBoolExpression(exprReuse)
	assertKind(t, expr.Kind(), ValueExpression)

	if expr.Expression() != exprReuse {
		t.Errorf("Expression() = %q, want %q", expr.Expression(), exprReuse)
	}

	if expr.Bool() {
		t.Error("Bool() on an expression should be false")
	}

	assertString(t, expr.String(), exprReuse)
}

func TestExprLongZeroValue(t *testing.T) {
	t.Parallel()

	zero := ExprLong{}
	assertKind(t, zero.Kind(), ValueUnset)

	if zero.IsSet() {
		t.Error("IsSet() = true on the zero ExprLong")
	}

	if zero.Int() != 0 || zero.Expression() != "" {
		t.Errorf("zero ExprLong returned a non-zero payload: %+v", zero)
	}
}

func TestExprLong(t *testing.T) {
	t.Parallel()

	literal := NewExprLong(-3)
	assertKind(t, literal.Kind(), ValueInt)

	if literal.Int() != -3 {
		t.Errorf("Int() = %d, want -3", literal.Int())
	}

	assertString(t, literal.String(), "-3")

	expr := NewExprLongExpression(exprCount)
	assertKind(t, expr.Kind(), ValueExpression)

	if expr.Int() != 0 {
		t.Errorf("Int() on an expression = %d, want 0", expr.Int())
	}

	assertString(t, expr.String(), exprCount)
}

func TestResourceValueUnsetIsNotZero(t *testing.T) {
	t.Parallel()

	// An unset ResourceRequirement field must stay distinguishable from an
	// explicit zero: the schema omits the minima's defaults precisely so an
	// implementation can tell them apart.
	zero := ResourceValue{}
	if zero.IsSet() {
		t.Error("zero ResourceValue reports IsSet")
	}

	if _, ok := zero.Number(); ok {
		t.Error("zero ResourceValue produced a number")
	}

	if !NewResourceInt(0).IsSet() {
		t.Error("an explicit 0 must report IsSet")
	}
}

func TestResourceValue(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		wantString string
		value      ResourceValue
		wantNumber float64
		wantKind   ValueKind
		wantOK     bool
	}{
		{
			name: kindNameInt, value: NewResourceInt(4), wantKind: ValueInt,
			wantNumber: 4, wantOK: true, wantString: "4",
		},
		{
			name: kindNameFloat, value: NewResourceFloat(0.5), wantKind: ValueFloat,
			wantNumber: 0.5, wantOK: true, wantString: "0.5",
		},
		{
			name: kindNameExpression, value: NewResourceExpression("$(2*3)"), wantKind: ValueExpression,
			wantNumber: 0, wantOK: false, wantString: "$(2*3)",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assertKind(t, tc.value.Kind(), tc.wantKind)

			num, ok := tc.value.Number()
			if ok != tc.wantOK || num != tc.wantNumber {
				t.Errorf("Number() = (%v, %v), want (%v, %v)", num, ok, tc.wantNumber, tc.wantOK)
			}

			assertString(t, tc.value.String(), tc.wantString)
		})
	}
}

func TestResourceValueAccessorsAreKindGated(t *testing.T) {
	t.Parallel()

	f := NewResourceFloat(2.5)
	if f.Int() != 0 {
		t.Errorf("Int() on a float value = %d, want 0", f.Int())
	}

	i := NewResourceInt(7)
	if i.Float() != 0 {
		t.Errorf("Float() on an int value = %v, want 0", i.Float())
	}

	if i.Expression() != "" {
		t.Errorf("Expression() on an int value = %q, want empty", i.Expression())
	}
}

func TestCommandLineArgumentZeroValue(t *testing.T) {
	t.Parallel()

	zero := CommandLineArgument{}
	assertKind(t, zero.Kind(), ValueUnset)

	if zero.Literal() != "" || zero.Expression() != "" || zero.Binding() != nil {
		t.Errorf("zero CommandLineArgument leaked a payload: %+v", zero)
	}
}

func TestCommandLineArgumentString(t *testing.T) {
	t.Parallel()

	literal := NewCommandLineArgumentString("--verbose")
	assertKind(t, literal.Kind(), ValueString)

	if literal.Literal() != "--verbose" {
		t.Errorf("Literal() = %q, want %q", literal.Literal(), "--verbose")
	}

	// A plain literal must not masquerade as an expression: decoding is what
	// separates the two, and the kind records the decision.
	if literal.Expression() != "" {
		t.Errorf("Expression() on a string literal = %q, want empty", literal.Expression())
	}
}

func TestCommandLineArgumentExpression(t *testing.T) {
	t.Parallel()

	expr := NewCommandLineArgumentExpression(exprFlag)
	assertKind(t, expr.Kind(), ValueExpression)

	if expr.Expression() != exprFlag {
		t.Errorf("Expression() = %q, want %q", expr.Expression(), exprFlag)
	}

	if expr.Literal() != "" {
		t.Errorf("Literal() on an expression = %q, want empty", expr.Literal())
	}
}

func TestCommandLineArgumentBinding(t *testing.T) {
	t.Parallel()

	binding := &CommandLineBinding{Prefix: "-o", Position: NewExprLong(2)}

	arg := NewCommandLineArgumentBinding(binding)
	assertKind(t, arg.Kind(), ValueBinding)

	if arg.Binding() != binding {
		t.Error("Binding() did not round-trip")
	}

	if arg.Literal() != "" || arg.Expression() != "" {
		t.Error("a binding argument leaked a string payload")
	}
}

func TestInitialWorkDirEntryKinds(t *testing.T) {
	t.Parallel()

	dirent := &Dirent{Entryname: direntName, Entry: "$(inputs.body)"}
	file := &File{Basename: direntName}
	dir := &Directory{Basename: "sub"}

	tests := []struct {
		name  string
		entry InitialWorkDirEntry
		want  ValueKind
	}{
		{name: kindNameUnset, entry: InitialWorkDirEntry{}, want: ValueUnset},
		{name: kindNameNull, entry: NewInitialWorkDirNull(), want: ValueNull},
		{name: kindNameDirent, entry: NewInitialWorkDirDirent(dirent), want: ValueDirent},
		{name: kindNameExpression, entry: NewInitialWorkDirExpression("$(inputs.f)"), want: ValueExpression},
		{name: kindNameFile, entry: NewInitialWorkDirFile(file), want: ValueFile},
		{name: kindNameDirectory, entry: NewInitialWorkDirDirectory(dir), want: ValueDirectory},
		{
			name:  kindNameList,
			entry: NewInitialWorkDirObjects([]FileOrDirectory{file, dir}),
			want:  ValueList,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assertKind(t, tc.entry.Kind(), tc.want)
		})
	}
}

func TestInitialWorkDirEntryPayloads(t *testing.T) {
	t.Parallel()

	dirent := &Dirent{Entryname: direntName, Entry: "$(inputs.body)"}
	file := &File{Basename: direntName}
	dir := &Directory{Basename: "sub"}

	if NewInitialWorkDirDirent(dirent).Dirent() != dirent {
		t.Error("Dirent() did not round-trip")
	}

	if NewInitialWorkDirFile(file).File() != file {
		t.Error("File() did not round-trip")
	}

	if NewInitialWorkDirDirectory(dir).Directory() != dir {
		t.Error("Directory() did not round-trip")
	}

	if got := NewInitialWorkDirObjects([]FileOrDirectory{file, dir}).Objects(); len(got) != 2 {
		t.Errorf("Objects() has %d entries, want 2", len(got))
	}

	// The payload accessors share one field, so each must be kind-gated.
	if NewInitialWorkDirNull().Dirent() != nil {
		t.Error("a null entry produced a Dirent")
	}

	if NewInitialWorkDirFile(file).Directory() != nil || NewInitialWorkDirFile(file).Dirent() != nil {
		t.Error("a File entry answered a non-File accessor")
	}

	if NewInitialWorkDirDirectory(dir).File() != nil || NewInitialWorkDirDirectory(dir).Objects() != nil {
		t.Error("a Directory entry answered a non-Directory accessor")
	}
}

func TestInitialWorkDirListingZeroValue(t *testing.T) {
	t.Parallel()

	zero := InitialWorkDirListing{}
	assertKind(t, zero.Kind(), ValueUnset)

	if zero.Entries() != nil || zero.Expression() != "" {
		t.Errorf("zero InitialWorkDirListing leaked a payload: %+v", zero)
	}
}

func TestInitialWorkDirListing(t *testing.T) {
	t.Parallel()

	entries := []InitialWorkDirEntry{
		NewInitialWorkDirExpression("$(inputs.a)"),
		NewInitialWorkDirNull(),
	}

	listing := NewInitialWorkDirListing(entries)
	assertKind(t, listing.Kind(), ValueList)

	if len(listing.Entries()) != 2 {
		t.Fatalf("Entries() has %d entries, want 2", len(listing.Entries()))
	}

	// Order is preserved: the listing is staged in document order.
	if listing.Entries()[0].Kind() != ValueExpression || listing.Entries()[1].Kind() != ValueNull {
		t.Error("listing entries were reordered")
	}

	// The whole listing may itself be one expression producing the array,
	// which is why the two forms cannot be flattened at this layer.
	fromExpr := NewInitialWorkDirListingExpression("${return inputs.files;}")
	assertKind(t, fromExpr.Kind(), ValueExpression)

	if fromExpr.Entries() != nil {
		t.Error("an expression listing produced entries")
	}
}

func TestValueKindString(t *testing.T) {
	t.Parallel()

	tests := []struct {
		want string
		kind ValueKind
	}{
		{kind: ValueUnset, want: kindNameUnset},
		{kind: ValueNull, want: kindNameNull},
		{kind: ValueBool, want: kindNameBool},
		{kind: ValueInt, want: kindNameInt},
		{kind: ValueFloat, want: kindNameFloat},
		{kind: ValueString, want: kindNameString},
		{kind: ValueExpression, want: kindNameExpression},
		{kind: ValueBinding, want: kindNameBinding},
		{kind: ValueDirent, want: kindNameDirent},
		{kind: ValueList, want: kindNameList},
		{kind: ValueFile, want: kindNameFile},
		{kind: ValueDirectory, want: kindNameDirectory},
	}

	for _, tc := range tests {
		if got := tc.kind.String(); got != tc.want {
			t.Errorf("ValueKind(%d).String() = %q, want %q", tc.kind, got, tc.want)
		}
	}

	if got := ValueKind(200).String(); got != "ValueKind(200)" {
		t.Errorf("out-of-range ValueKind rendered %q", got)
	}
}

func TestCommandLineArgumentStringRendering(t *testing.T) {
	t.Parallel()

	binding := &CommandLineBinding{Prefix: "-o"}

	tests := []struct {
		name string
		want string
		arg  CommandLineArgument
	}{
		{name: kindNameUnset, arg: CommandLineArgument{}, want: kindNameUnset},
		{name: kindNameString, arg: NewCommandLineArgumentString("--x"), want: "--x"},
		{name: kindNameExpression, arg: NewCommandLineArgumentExpression(exprFlag), want: exprFlag},
		{name: kindNameBinding, arg: NewCommandLineArgumentBinding(binding), want: fmt.Sprintf("%+v", binding)},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assertString(t, tc.arg.String(), tc.want)
		})
	}
}

func TestInitialWorkDirEntryStringRendering(t *testing.T) {
	t.Parallel()

	dirent := &Dirent{Entryname: direntName}
	file := &File{Basename: direntName}

	tests := []struct {
		name  string
		want  string
		entry InitialWorkDirEntry
	}{
		{name: kindNameNull, entry: NewInitialWorkDirNull(), want: kindNameNull},
		{name: kindNameExpression, entry: NewInitialWorkDirExpression(exprFlag), want: exprFlag},
		{name: kindNameDirent, entry: NewInitialWorkDirDirent(dirent), want: fmt.Sprintf("%+v", dirent)},
		{name: kindNameFile, entry: NewInitialWorkDirFile(file), want: fmt.Sprintf("%+v", file)},
		{
			name:  kindNameList,
			entry: NewInitialWorkDirObjects([]FileOrDirectory{file}),
			want:  "[1 objects]",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assertString(t, tc.entry.String(), tc.want)
		})
	}

	// Expression is kind-gated like every other payload accessor.
	if NewInitialWorkDirNull().Expression() != "" {
		t.Error("a null entry produced an expression")
	}
}

func TestInitialWorkDirListingStringRendering(t *testing.T) {
	t.Parallel()

	entries := []InitialWorkDirEntry{NewInitialWorkDirNull(), NewInitialWorkDirNull()}

	tests := []struct {
		name    string
		want    string
		listing InitialWorkDirListing
	}{
		{name: kindNameUnset, listing: InitialWorkDirListing{}, want: kindNameUnset},
		{name: kindNameExpression, listing: NewInitialWorkDirListingExpression(exprFlag), want: exprFlag},
		{name: kindNameList, listing: NewInitialWorkDirListing(entries), want: "[2 entries]"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assertString(t, tc.listing.String(), tc.want)
		})
	}
}

func TestExprWrapperStringFallbacks(t *testing.T) {
	t.Parallel()

	// The String methods fall back to the kind name for every member they do
	// not render specially, including the zero value.
	assertString(t, ExprBool{}.String(), kindNameUnset)
	assertString(t, ExprLong{}.String(), kindNameUnset)
	assertString(t, ResourceValue{}.String(), kindNameUnset)
}

func TestOptInt(t *testing.T) {
	t.Parallel()

	zero := OptInt{}
	if zero.IsSet() {
		t.Error("the zero OptInt reports IsSet")
	}

	if zero.Int() != 0 || zero.Or(7) != 7 {
		t.Errorf("zero OptInt: Int() = %d, Or(7) = %d", zero.Int(), zero.Or(7))
	}

	assertString(t, zero.String(), kindNameUnset)

	// The point of OptInt: an explicit 0 — an empty file — must not read as
	// "size not computed".
	empty := NewOptInt(0)
	if !empty.IsSet() {
		t.Error("an explicit 0 must report IsSet")
	}

	if empty.Or(7) != 0 {
		t.Errorf("Or(7) on an explicit 0 = %d, want 0", empty.Or(7))
	}

	assertString(t, empty.String(), "0")
	assertString(t, NewOptInt(1024).String(), "1024")
}

func TestOptString(t *testing.T) {
	t.Parallel()

	zero := OptString{}
	if zero.IsSet() {
		t.Error("the zero OptString reports IsSet")
	}

	if zero.Value() != "" || zero.Or("x") != "x" {
		t.Errorf("zero OptString: Value() = %q, Or(\"x\") = %q", zero.Value(), zero.Or("x"))
	}

	assertString(t, zero.String(), kindNameUnset)

	// The point of OptString: contents "" is an empty file literal, not an
	// unread file.
	empty := NewOptString("")
	if !empty.IsSet() {
		t.Error("an explicit empty string must report IsSet")
	}

	if empty.Or("x") != "" {
		t.Errorf("Or on an explicit empty string = %q, want empty", empty.Or("x"))
	}

	if got := NewOptString("hello").Value(); got != "hello" {
		t.Errorf("Value() = %q, want %q", got, "hello")
	}

	assertString(t, NewOptString("hello").String(), "hello")
}
