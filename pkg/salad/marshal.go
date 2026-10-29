package salad

import (
	"bytes"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Marshal serializes a salad Node tree to YAML.
func Marshal(n Node) ([]byte, error) {
	yn := nodeToYAML(n)

	doc := &yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{yn}}

	var b bytes.Buffer

	enc := yaml.NewEncoder(&b)
	enc.SetIndent(2)

	err := enc.Encode(doc)
	if err != nil {
		return nil, err
	}

	err = enc.Close()
	if err != nil {
		return nil, err
	}

	return b.Bytes(), nil
}

func nodeToYAML(n Node) *yaml.Node {
	if n == nil {
		return nullYAML()
	}

	switch v := n.(type) {
	case *MapNode:
		return mapToYAML(v)
	case *SeqNode:
		return seqToYAML(v)
	case *ScalarNode:
		return scalarToYAML(v)
	default:
		return nullYAML()
	}
}

func mapToYAML(m *MapNode) *yaml.Node {
	yn := &yaml.Node{Kind: yaml.MappingNode}

	for _, e := range m.Entries() {
		yn.Content = append(yn.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Value: e.Key},
			nodeToYAML(e.Value),
		)
	}

	return yn
}

func seqToYAML(s *SeqNode) *yaml.Node {
	yn := &yaml.Node{Kind: yaml.SequenceNode}

	for i := range s.Len() {
		yn.Content = append(yn.Content, nodeToYAML(s.At(i)))
	}

	return yn
}

func scalarToYAML(s *ScalarNode) *yaml.Node {
	if s == nil {
		return nullYAML()
	}

	switch s.Kind() {
	case NullScalar:
		return nullYAML()
	case BoolScalar:
		return &yaml.Node{
			Kind:  yaml.ScalarNode,
			Tag:   "!!bool",
			Value: strconv.FormatBool(s.AsBool()),
		}
	case IntScalar:
		v, _ := s.AsInt()

		return &yaml.Node{
			Kind:  yaml.ScalarNode,
			Tag:   "!!int",
			Value: strconv.FormatInt(v, 10),
		}
	case FloatScalar:
		v, _ := s.AsFloat()

		return &yaml.Node{
			Kind:  yaml.ScalarNode,
			Tag:   "!!float",
			Value: strconv.FormatFloat(v, 'g', -1, 64),
		}
	case DecimalScalar:
		d, _ := s.AsDecimal()

		return &yaml.Node{Kind: yaml.ScalarNode, Value: d.String()}
	default:
		v, _ := s.AsString()
		n := &yaml.Node{Kind: yaml.ScalarNode, Value: v}

		if needsQuoting(v) {
			n.Style = yaml.DoubleQuotedStyle
		}

		return n
	}
}

var yamlKeywords = map[string]bool{ //nolint:gochecknoglobals // YAML 1.1/1.2 reserved words
	"null": true, "Null": true, "NULL": true, "~": true,
	"true": true, "True": true, "TRUE": true,
	"false": true, "False": true, "FALSE": true,
	"yes": true, "Yes": true, "YES": true,
	"no": true, "No": true, "NO": true,
	"on": true, "On": true, "ON": true,
	"off": true, "Off": true, "OFF": true,
}

func needsQuoting(s string) bool {
	return strings.Contains(s, "#") || yamlKeywords[s]
}

func nullYAML() *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!null", Value: "null"}
}
