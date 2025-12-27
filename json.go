package main

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/feather-lang/feather"
)

// SchemaNode represents a node in the JSON schema
type SchemaNode struct {
	Type     string        // "string", "number", "bool", "object", "array"
	Name     string        // field name
	Children []*SchemaNode // for object: fields; for array: single element describing item type
}

// parseSchema parses the schema DSL into a tree of SchemaNodes
func parseSchema(schemaStr string) ([]*SchemaNode, error) {
	tokens := tokenizeSchema(schemaStr)
	nodes, _, err := parseSchemaTokens(tokens, 0)
	return nodes, err
}

func tokenizeSchema(s string) []string {
	var tokens []string
	var current strings.Builder

	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		case '{':
			if current.Len() > 0 {
				tokens = append(tokens, current.String())
				current.Reset()
			}
			tokens = append(tokens, "{")
		case '}':
			if current.Len() > 0 {
				tokens = append(tokens, current.String())
				current.Reset()
			}
			tokens = append(tokens, "}")
		case ' ', '\t', '\n', '\r', ';':
			if current.Len() > 0 {
				tokens = append(tokens, current.String())
				current.Reset()
			}
		default:
			current.WriteByte(c)
		}
	}
	if current.Len() > 0 {
		tokens = append(tokens, current.String())
	}
	return tokens
}

func parseSchemaTokens(tokens []string, pos int) ([]*SchemaNode, int, error) {
	var nodes []*SchemaNode

	for pos < len(tokens) {
		token := tokens[pos]

		if token == "}" {
			return nodes, pos, nil
		}

		if token == "{" {
			pos++
			continue
		}

		// Expect a type
		switch token {
		case "string", "number", "bool":
			if pos+1 >= len(tokens) {
				return nil, pos, fmt.Errorf("expected field name after %s", token)
			}
			node := &SchemaNode{Type: token, Name: tokens[pos+1]}
			nodes = append(nodes, node)
			pos += 2

		case "object":
			if pos+1 >= len(tokens) {
				return nil, pos, fmt.Errorf("expected field name after object")
			}
			name := tokens[pos+1]
			pos += 2
			if pos >= len(tokens) || tokens[pos] != "{" {
				return nil, pos, fmt.Errorf("expected { after object %s", name)
			}
			pos++ // skip {
			children, newPos, err := parseSchemaTokens(tokens, pos)
			if err != nil {
				return nil, newPos, err
			}
			pos = newPos + 1 // skip }
			node := &SchemaNode{Type: "object", Name: name, Children: children}
			nodes = append(nodes, node)

		case "array":
			if pos+1 >= len(tokens) {
				return nil, pos, fmt.Errorf("expected field name after array")
			}
			name := tokens[pos+1]
			pos += 2
			if pos >= len(tokens) {
				return nil, pos, fmt.Errorf("expected element type after array %s", name)
			}
			elemType := tokens[pos]
			pos++

			var elemNode *SchemaNode
			if elemType == "object" {
				if pos >= len(tokens) || tokens[pos] != "{" {
					return nil, pos, fmt.Errorf("expected { after array %s object", name)
				}
				pos++ // skip {
				children, newPos, err := parseSchemaTokens(tokens, pos)
				if err != nil {
					return nil, newPos, err
				}
				pos = newPos + 1 // skip }
				elemNode = &SchemaNode{Type: "object", Children: children}
			} else {
				elemNode = &SchemaNode{Type: elemType}
			}

			node := &SchemaNode{Type: "array", Name: name, Children: []*SchemaNode{elemNode}}
			nodes = append(nodes, node)

		default:
			return nil, pos, fmt.Errorf("unexpected token: %s", token)
		}
	}

	return nodes, pos, nil
}

// encodeWithSchema encodes a feather dict/list according to the schema
func encodeWithSchema(obj *feather.Obj, schema []*SchemaNode) (string, error) {
	dict, err := feather.AsDict(obj)
	if err != nil {
		return "", fmt.Errorf("expected dict for object encoding: %v", err)
	}

	var parts []string
	for _, node := range schema {
		val, ok := dict.Items[node.Name]
		if !ok {
			continue // skip missing fields
		}

		encoded, err := encodeValue(val, node)
		if err != nil {
			return "", fmt.Errorf("field %s: %v", node.Name, err)
		}
		parts = append(parts, fmt.Sprintf("%q:%s", node.Name, encoded))
	}

	return "{" + strings.Join(parts, ",") + "}", nil
}

func encodeValue(val *feather.Obj, node *SchemaNode) (string, error) {
	switch node.Type {
	case "string":
		// JSON-encode the string
		b, _ := json.Marshal(val.String())
		return string(b), nil

	case "number":
		s := val.String()
		// Validate it's a number
		if _, err := strconv.ParseFloat(s, 64); err != nil {
			return "", fmt.Errorf("invalid number: %s", s)
		}
		return s, nil

	case "bool":
		s := val.String()
		switch s {
		case "1", "true":
			return "true", nil
		case "0", "false":
			return "false", nil
		default:
			return "", fmt.Errorf("invalid bool: %s", s)
		}

	case "object":
		return encodeWithSchema(val, node.Children)

	case "array":
		list, err := val.List()
		if err != nil {
			return "", fmt.Errorf("expected list for array: %v", err)
		}
		elemNode := node.Children[0]
		var items []string
		for i, item := range list {
			encoded, err := encodeValue(item, elemNode)
			if err != nil {
				return "", fmt.Errorf("index %d: %v", i, err)
			}
			items = append(items, encoded)
		}
		return "[" + strings.Join(items, ",") + "]", nil

	default:
		return "", fmt.Errorf("unknown type: %s", node.Type)
	}
}

// decodeWithSchema decodes JSON into a feather-compatible structure according to schema
func decodeWithSchema(jsonStr string, schema []*SchemaNode) (map[string]any, error) {
	var raw map[string]any
	if err := json.Unmarshal([]byte(jsonStr), &raw); err != nil {
		return nil, err
	}
	return decodeObject(raw, schema)
}

func decodeObject(raw map[string]any, schema []*SchemaNode) (map[string]any, error) {
	result := make(map[string]any)
	for _, node := range schema {
		val, ok := raw[node.Name]
		if !ok {
			continue
		}
		decoded, err := decodeValue(val, node)
		if err != nil {
			return nil, fmt.Errorf("field %s: %v", node.Name, err)
		}
		result[node.Name] = decoded
	}
	return result, nil
}

func decodeValue(val any, node *SchemaNode) (any, error) {
	switch node.Type {
	case "string":
		if s, ok := val.(string); ok {
			return s, nil
		}
		return fmt.Sprintf("%v", val), nil

	case "number":
		switch v := val.(type) {
		case float64:
			if v == float64(int64(v)) {
				return fmt.Sprintf("%d", int64(v)), nil
			}
			return fmt.Sprintf("%g", v), nil
		case string:
			return v, nil
		default:
			return fmt.Sprintf("%v", v), nil
		}

	case "bool":
		switch v := val.(type) {
		case bool:
			if v {
				return "1", nil
			}
			return "0", nil
		default:
			return fmt.Sprintf("%v", v), nil
		}

	case "object":
		obj, ok := val.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("expected object, got %T", val)
		}
		return decodeObject(obj, node.Children)

	case "array":
		arr, ok := val.([]any)
		if !ok {
			return nil, fmt.Errorf("expected array, got %T", val)
		}
		elemNode := node.Children[0]
		var items []any
		for i, item := range arr {
			decoded, err := decodeValue(item, elemNode)
			if err != nil {
				return nil, fmt.Errorf("index %d: %v", i, err)
			}
			items = append(items, decoded)
		}
		return items, nil

	default:
		return nil, fmt.Errorf("unknown type: %s", node.Type)
	}
}

func registerJSONCommand(fi *feather.Interp, state *ServerState) {
	jsonCmd := &Command{
		Name:  "json",
		Help:  "Encode or decode JSON with schema",
		Usage: "json VALUE -as SCHEMA | json VALUE -from SCHEMA",
		Subcommands: []*Command{
			{Name: "-as", Help: "Encode TCL value to JSON using schema", Usage: "json VALUE -as SCHEMA"},
			{Name: "-from", Help: "Decode JSON string to TCL value using schema", Usage: "json VALUE -from SCHEMA"},
		},
	}
	registry.Register(jsonCmd)

	// Use low-level registration to avoid TCL quoting of JSON output
	fi.Internal().Register("json", func(i *feather.InternalInterp, cmd feather.FeatherObj, args []feather.FeatherObj) feather.FeatherResult {
		if len(args) < 3 {
			i.SetErrorString("wrong # args: should be \"json value -as schema\" or \"json value -from schema\"")
			return feather.ResultError
		}

		flag := i.GetString(args[1])
		schemaStr := i.GetString(args[2])

		schema, err := parseSchema(schemaStr)
		if err != nil {
			i.SetErrorString(fmt.Sprintf("json: invalid schema: %v", err))
			return feather.ResultError
		}

		switch flag {
		case "-as":
			// Encode TCL dict to JSON directly to buffer
			dictVal, _, err := i.GetDict(args[0])
			if err != nil {
				i.SetErrorString(fmt.Sprintf("json: expected dict: %v", err))
				return feather.ResultError
			}
			enc := newJSONEncoder(i)
			if err := enc.encodeDict(dictVal, schema); err != nil {
				i.SetErrorString(fmt.Sprintf("json: encode error: %v", err))
				return feather.ResultError
			}
			i.SetResult(i.InternString(enc.String()))
			return feather.ResultOK

		case "-from":
			// Decode JSON to TCL dict
			jsonStr := i.GetString(args[0])
			decoded, err := decodeWithSchema(jsonStr, schema)
			if err != nil {
				i.SetErrorString(fmt.Sprintf("json: decode error: %v", err))
				return feather.ResultError
			}
			// Build dict result
			dict := i.NewDict()
			for k, v := range decoded {
				dict = setDictValue(i, dict, k, v)
			}
			i.SetResult(dict)
			return feather.ResultOK

		default:
			i.SetErrorString(fmt.Sprintf("json: unknown flag %q (use -as or -from)", flag))
			return feather.ResultError
		}
	})
}

// jsonEncoder writes JSON directly to a buffer based on schema
type jsonEncoder struct {
	i   *feather.InternalInterp
	buf *strings.Builder
}

func newJSONEncoder(i *feather.InternalInterp) *jsonEncoder {
	return &jsonEncoder{i: i, buf: &strings.Builder{}}
}

func (e *jsonEncoder) String() string {
	return e.buf.String()
}

func (e *jsonEncoder) encodeDict(dict map[string]feather.FeatherObj, schema []*SchemaNode) error {
	e.buf.WriteByte('{')
	first := true
	for _, node := range schema {
		val, ok := dict[node.Name]
		if !ok {
			continue
		}
		if !first {
			e.buf.WriteByte(',')
		}
		first = false
		e.buf.WriteByte('"')
		e.buf.WriteString(node.Name)
		e.buf.WriteString("\":")
		if err := e.encodeValue(val, node); err != nil {
			return fmt.Errorf("field %s: %v", node.Name, err)
		}
	}
	e.buf.WriteByte('}')
	return nil
}

func (e *jsonEncoder) encodeValue(val feather.FeatherObj, node *SchemaNode) error {
	switch node.Type {
	case "string":
		s := e.getRawString(val)
		b, _ := json.Marshal(s)
		e.buf.Write(b)
		return nil

	case "number":
		s := e.getRawString(val)
		if _, err := strconv.ParseFloat(s, 64); err != nil {
			return fmt.Errorf("invalid number: %s", s)
		}
		e.buf.WriteString(s)
		return nil

	case "bool":
		s := e.getRawString(val)
		switch s {
		case "1", "true":
			e.buf.WriteString("true")
		case "0", "false":
			e.buf.WriteString("false")
		default:
			return fmt.Errorf("invalid bool: %s", s)
		}
		return nil

	case "object":
		dictVal, _, err := e.i.GetDict(val)
		if err != nil {
			return fmt.Errorf("expected dict for object: %v", err)
		}
		return e.encodeDict(dictVal, node.Children)

	case "array":
		list, err := e.i.GetList(val)
		if err != nil {
			return fmt.Errorf("expected list for array: %v", err)
		}
		elemNode := node.Children[0]
		e.buf.WriteByte('[')
		for idx, item := range list {
			if idx > 0 {
				e.buf.WriteByte(',')
			}
			if err := e.encodeValue(item, elemNode); err != nil {
				return fmt.Errorf("index %d: %v", idx, err)
			}
		}
		e.buf.WriteByte(']')
		return nil

	default:
		return fmt.Errorf("unknown type: %s", node.Type)
	}
}

// getRawString extracts the raw string value, stripping Tcl braces if present
func (e *jsonEncoder) getRawString(val feather.FeatherObj) string {
	s := e.i.GetString(val)
	// Strip Tcl braces that wrap strings with spaces
	if len(s) >= 2 && s[0] == '{' && s[len(s)-1] == '}' {
		return s[1 : len(s)-1]
	}
	return s
}

func setDictValue(i *feather.InternalInterp, dict feather.FeatherObj, key string, val any) feather.FeatherObj {
	switch v := val.(type) {
	case string:
		return i.DictSet(dict, key, i.InternString(v))
	case map[string]any:
		subDict := i.NewDict()
		for k, sv := range v {
			subDict = setDictValue(i, subDict, k, sv)
		}
		return i.DictSet(dict, key, subDict)
	case []any:
		list := i.NewList()
		for _, item := range v {
			switch iv := item.(type) {
			case string:
				list = i.ListAppend(list, i.InternString(iv))
			case map[string]any:
				subDict := i.NewDict()
				for k, sv := range iv {
					subDict = setDictValue(i, subDict, k, sv)
				}
				list = i.ListAppend(list, subDict)
			default:
				list = i.ListAppend(list, i.InternString(fmt.Sprintf("%v", iv)))
			}
		}
		return i.DictSet(dict, key, list)
	default:
		return i.DictSet(dict, key, i.InternString(fmt.Sprintf("%v", v)))
	}
}
