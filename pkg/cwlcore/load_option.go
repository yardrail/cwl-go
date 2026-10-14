package cwlcore

import "github.com/yardrail/cwl-go/pkg/salad"

// loadConfig holds the resolved configuration for a Load call.
type loadConfig struct {
	validateOpts []salad.ValidateOption
	extensions   []*salad.LoadedSchema
	premerged    *salad.LoadedSchema
}

// LoadOption configures a Load call.
type LoadOption func(*loadConfig)

// WithValidateOptions passes salad validation options through to the schema
// validator.
func WithValidateOptions(opts ...salad.ValidateOption) LoadOption {
	return func(c *loadConfig) {
		c.validateOpts = append(c.validateOpts, opts...)
	}
}

// Strict is shorthand for WithValidateOptions(salad.Strict(strict)).
func Strict(strict bool) LoadOption {
	return func(c *loadConfig) {
		c.validateOpts = append(c.validateOpts, salad.Strict(strict))
	}
}

// WithExtensionSchema merges an extension Schema Salad schema into the CWL type
// graph before validation. Extension types that extend Process become valid
// document roots and valid inline run targets, and their fields are
// schema-validated. The extension must have been loaded with salad.LoadSchema.
func WithExtensionSchema(ext *salad.LoadedSchema) LoadOption {
	return func(c *loadConfig) {
		c.extensions = append(c.extensions, ext)
	}
}

// WithPremergedSchema supplies a schema that already includes the CWL base
// merged with all extension schemas. When set, Load skips schemaFor and the
// per-extension MergeSchemas loop entirely, using this schema directly.
// Build one with [MergeExtensionSchemas].
func WithPremergedSchema(merged *salad.LoadedSchema) LoadOption {
	return func(c *loadConfig) {
		c.premerged = merged
	}
}

func buildLoadConfig(opts []LoadOption) *loadConfig {
	cfg := &loadConfig{validateOpts: nil, extensions: nil, premerged: nil}
	for _, o := range opts {
		o(cfg)
	}

	return cfg
}
