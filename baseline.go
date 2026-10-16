package main

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"
)

//go:embed compatibility/baseline-1.27.json
var baselineJSON []byte

type baselineSchema struct {
	Ref        string                    `json:"$ref"`
	Type       string                    `json:"type"`
	Properties map[string]baselineSchema `json:"properties"`
	Items      *baselineSchema           `json:"items"`
	Additional json.RawMessage           `json:"additionalProperties"`
	Required   []string                  `json:"required"`
}

func validateBaseline(object map[string]interface{}, root string) error {
	var catalog struct {
		Definitions map[string]baselineSchema `json:"definitions"`
	}
	if err := json.Unmarshal(baselineJSON, &catalog); err != nil {
		return err
	}
	var visit func(interface{}, baselineSchema, string) error
	visit = func(value interface{}, s baselineSchema, path string) error {
		if value == nil {
			return nil
		}
		if s.Ref != "" {
			// Swagger does not enumerate this string field. New SDKs know
			// comparison operators that the minimum server cannot interpret.
			if s.Ref == "#/definitions/io.k8s.api.core.v1.Toleration" {
				if toleration, ok := value.(map[string]interface{}); ok {
					if op, found := toleration["operator"]; found && op != nil && op != "" && op != "Equal" && op != "Exists" {
						return fmt.Errorf("%s.operator %v is not in Kubernetes 1.27 baseline; use Equal/Exists or review migration manually", path, op)
					}
				}
			}
			var ok bool
			s, ok = catalog.Definitions[strings.TrimPrefix(s.Ref, "#/definitions/")]
			if !ok {
				return fmt.Errorf("unknown baseline definition at %s", path)
			}
		}
		switch x := value.(type) {
		case map[string]interface{}:
			for _, req := range s.Required {
				if _, ok := x[req]; !ok {
					return fmt.Errorf("baseline requires %s.%s", path, req)
				}
			}
			for key, v := range x {
				child, known := s.Properties[key]
				if !known {
					if len(s.Additional) > 0 && string(s.Additional) != "false" {
						if string(s.Additional) == "true" {
							continue
						}
						if err := json.Unmarshal(s.Additional, &child); err != nil {
							return err
						}
					} else if len(s.Properties) > 0 {
						return fmt.Errorf("%s.%s is not in Kubernetes 1.27 baseline", path, key)
					} else {
						continue
					}
				}
				if err := visit(v, child, path+"."+key); err != nil {
					return err
				}
			}
		case []interface{}:
			if s.Items != nil {
				for i, v := range x {
					if err := visit(v, *s.Items, fmt.Sprintf("%s[%d]", path, i)); err != nil {
						return err
					}
				}
			}
		}
		return nil
	}
	s, ok := catalog.Definitions[root]
	if !ok {
		return fmt.Errorf("unknown baseline root %s", root)
	}
	return visit(object, s, "$")
}
