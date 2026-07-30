package ledger

import (
	"bytes"
	"embed"
	"fmt"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

//go:embed schema/usage.v1.schema.json
var schemaFS embed.FS

func loadSchema() (*jsonschema.Schema, error) {
	c := jsonschema.NewCompiler()
	f, err := schemaFS.Open("schema/usage.v1.schema.json")
	if err != nil {
		return nil, fmt.Errorf("open embedded schema: %w", err)
	}
	defer func() { _ = f.Close() }()

	doc, err := jsonschema.UnmarshalJSON(f)
	if err != nil {
		return nil, fmt.Errorf("unmarshal schema: %w", err)
	}
	if err := c.AddResource("usage.v1.schema.json", doc); err != nil {
		return nil, fmt.Errorf("add schema resource: %w", err)
	}
	return c.Compile("usage.v1.schema.json")
}

func validateEvent(schema *jsonschema.Schema, raw []byte) error {
	v, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		return fmt.Errorf("invalid JSON: %w", err)
	}
	return schema.Validate(v)
}
