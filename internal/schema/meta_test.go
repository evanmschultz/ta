package schema

import (
	"strings"
	"testing"
)

func TestMetaSchemaEmbeddedAndNonEmpty(t *testing.T) {
	if MetaSchemaTOML == "" {
		t.Fatal("MetaSchemaTOML is empty; embed directive is broken")
	}
	if !strings.Contains(MetaSchemaTOML, "[ta_schema]") {
		t.Errorf("MetaSchemaTOML missing [ta_schema] root: %s", MetaSchemaTOML[:min(200, len(MetaSchemaTOML))])
	}
}

func TestMetaSchemaLoadsUnderNewGrammar(t *testing.T) {
	reg, err := LoadBytes([]byte(MetaSchemaTOML))
	if err != nil {
		t.Fatalf("meta-schema must load under its own grammar: %v", err)
	}
	db, ok := reg.DBs["ta_schema"]
	if !ok {
		t.Fatal("ta_schema db missing from parsed meta-schema")
	}
	for _, want := range []string{"db", "type", "field"} {
		if _, ok := db.Types[want]; !ok {
			t.Errorf("meta-schema missing kind %q", want)
		}
	}
}

func TestMetaSchemaPathConstant(t *testing.T) {
	if MetaSchemaPath != "ta_schema" {
		t.Errorf("MetaSchemaPath = %q, want ta_schema", MetaSchemaPath)
	}
}
