package provider

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	dsschema "github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	provschema "github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	resschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
)

func TestProviderMetadata(t *testing.T) {
	p := New("1.2.3")()
	var resp provider.MetadataResponse
	p.Metadata(context.Background(), provider.MetadataRequest{}, &resp)
	if resp.TypeName != "janus" {
		t.Errorf("TypeName = %q, want janus", resp.TypeName)
	}
	if resp.Version != "1.2.3" {
		t.Errorf("Version = %q, want 1.2.3", resp.Version)
	}
}

func TestProviderSchemaValid(t *testing.T) {
	p := New("test")()
	var resp provider.SchemaResponse
	p.Schema(context.Background(), provider.SchemaRequest{}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("provider schema diagnostics: %v", resp.Diagnostics)
	}

	// token must be sensitive so it never prints in plan output.
	tokAttr, ok := resp.Schema.Attributes["token"].(provschema.StringAttribute)
	if !ok {
		t.Fatal("token attribute missing/wrong type")
	}
	if !tokAttr.Sensitive {
		t.Error("provider token attribute must be Sensitive")
	}
}

func TestProviderRegistersAllResourcesAndDataSources(t *testing.T) {
	p := New("test")().(*janusProvider)

	resources := p.Resources(context.Background())
	if len(resources) != 5 {
		t.Fatalf("want 5 resources, got %d", len(resources))
	}
	wantResTypes := map[string]bool{
		"janus_project":       false,
		"janus_environment":   false,
		"janus_config":        false,
		"janus_secret":        false,
		"janus_service_token": false,
	}
	for _, f := range resources {
		r := f()
		var mResp resource.MetadataResponse
		r.Metadata(context.Background(), resource.MetadataRequest{ProviderTypeName: "janus"}, &mResp)
		if _, ok := wantResTypes[mResp.TypeName]; !ok {
			t.Errorf("unexpected resource type %q", mResp.TypeName)
		}
		wantResTypes[mResp.TypeName] = true

		// Every resource schema must render without error.
		var sResp resource.SchemaResponse
		r.Schema(context.Background(), resource.SchemaRequest{}, &sResp)
		if sResp.Diagnostics.HasError() {
			t.Errorf("%s schema diagnostics: %v", mResp.TypeName, sResp.Diagnostics)
		}
		assertResourceSchema(t, mResp.TypeName, sResp.Schema)
	}
	for name, seen := range wantResTypes {
		if !seen {
			t.Errorf("resource %q not registered", name)
		}
	}

	ds := p.DataSources(context.Background())
	if len(ds) != 2 {
		t.Fatalf("want 2 data sources, got %d", len(ds))
	}
	wantDS := map[string]bool{"janus_secret": false, "janus_config": false}
	for _, f := range ds {
		d := f()
		var mResp datasource.MetadataResponse
		d.Metadata(context.Background(), datasource.MetadataRequest{ProviderTypeName: "janus"}, &mResp)
		if _, ok := wantDS[mResp.TypeName]; !ok {
			t.Errorf("unexpected data source %q", mResp.TypeName)
		}
		wantDS[mResp.TypeName] = true
		var sResp datasource.SchemaResponse
		d.Schema(context.Background(), datasource.SchemaRequest{}, &sResp)
		if sResp.Diagnostics.HasError() {
			t.Errorf("%s data source schema diagnostics: %v", mResp.TypeName, sResp.Diagnostics)
		}
	}
	for name, seen := range wantDS {
		if !seen {
			t.Errorf("data source %q not registered", name)
		}
	}
}

// TestSensitiveAttributes asserts every secret-bearing attribute is Sensitive.
func TestSensitiveAttributes(t *testing.T) {
	// janus_secret.value
	var sResp resource.SchemaResponse
	NewSecretResource().Schema(context.Background(), resource.SchemaRequest{}, &sResp)
	assertResAttrSensitive(t, sResp.Schema, "value")

	// janus_service_token.token
	var tResp resource.SchemaResponse
	NewServiceTokenResource().Schema(context.Background(), resource.SchemaRequest{}, &tResp)
	assertResAttrSensitive(t, tResp.Schema, "token")

	// data.janus_secret.value
	var dResp datasource.SchemaResponse
	NewSecretDataSource().Schema(context.Background(), datasource.SchemaRequest{}, &dResp)
	dAttr, ok := dResp.Schema.Attributes["value"].(dsschema.StringAttribute)
	if !ok || !dAttr.Sensitive {
		t.Error("data.janus_secret.value must be Sensitive")
	}
}

func assertResAttrSensitive(t *testing.T, s resschema.Schema, name string) {
	t.Helper()
	attr, ok := s.Attributes[name].(resschema.StringAttribute)
	if !ok {
		t.Fatalf("attribute %q missing/wrong type", name)
	}
	if !attr.Sensitive {
		t.Errorf("attribute %q must be Sensitive", name)
	}
}

func assertResourceSchema(t *testing.T, name string, s resschema.Schema) {
	t.Helper()
	if len(s.Attributes) == 0 {
		t.Errorf("%s schema has no attributes", name)
	}
}
