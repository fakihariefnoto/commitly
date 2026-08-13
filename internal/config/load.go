package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// loadYAMLFile decodes a YAML file into a nested map and records the line
// number of each top-level key (for warnings that name the line).
func loadYAMLFile(path string) (map[string]any, map[string]int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	var node yaml.Node
	if err := yaml.Unmarshal(data, &node); err != nil {
		return nil, nil, err
	}
	if node.Kind == 0 {
		return map[string]any{}, map[string]int{}, nil
	}
	if node.Kind == yaml.DocumentNode && len(node.Content) > 0 {
		node = *node.Content[0]
	}
	if node.Kind != yaml.MappingNode {
		return nil, nil, fmt.Errorf("expected a mapping at the top level")
	}

	lineMap := map[string]int{}
	decoded := map[string]any{}
	for i := 0; i+1 < len(node.Content); i += 2 {
		keyNode := node.Content[i]
		valNode := node.Content[i+1]
		key := keyNode.Value
		lineMap[key] = keyNode.Line
		decoded[key] = nodeToAny(valNode)
	}
	return decoded, lineMap, nil
}

func nodeToAny(n *yaml.Node) any {
	switch n.Kind {
	case yaml.ScalarNode:
		var out any
		if err := n.Decode(&out); err != nil {
			return n.Value
		}
		return out
	case yaml.SequenceNode:
		out := make([]any, 0, len(n.Content))
		for _, c := range n.Content {
			out = append(out, nodeToAny(c))
		}
		return out
	case yaml.MappingNode:
		out := map[string]any{}
		for i := 0; i+1 < len(n.Content); i += 2 {
			out[n.Content[i].Value] = nodeToAny(n.Content[i+1])
		}
		return out
	default:
		return nil
	}
}
