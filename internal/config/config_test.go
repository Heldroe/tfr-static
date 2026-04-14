package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadFileConfig_AllFields(t *testing.T) {
	dir := t.TempDir()
	content := `
base_url       = "https://registry.example.com"
main_branch    = "master"
output_dir     = "dist"
modules_path   = "/v1/modules/"
html           = true
html_index     = "docs.html"
gzip           = true
terraform_docs = true
`
	os.WriteFile(filepath.Join(dir, ConfigFileName), []byte(content), 0o644)

	fc, err := LoadFileConfig(dir)
	if err != nil {
		t.Fatal(err)
	}
	if fc == nil {
		t.Fatal("expected non-nil config")
	}
	if *fc.BaseURL != "https://registry.example.com" {
		t.Errorf("BaseURL = %q", *fc.BaseURL)
	}
	if *fc.MainBranch != "master" {
		t.Errorf("MainBranch = %q", *fc.MainBranch)
	}
	if *fc.OutputDir != "dist" {
		t.Errorf("OutputDir = %q", *fc.OutputDir)
	}
	if *fc.ModulesPath != "/v1/modules/" {
		t.Errorf("ModulesPath = %q", *fc.ModulesPath)
	}
	if fc.HTML == nil || !*fc.HTML {
		t.Error("HTML should be true")
	}
	if *fc.HTMLIndex != "docs.html" {
		t.Errorf("HTMLIndex = %q", *fc.HTMLIndex)
	}
	if fc.Gzip == nil || !*fc.Gzip {
		t.Error("Gzip should be true")
	}
	if fc.TerraformDocs == nil || !*fc.TerraformDocs {
		t.Error("TerraformDocs should be true")
	}
}

func TestLoadFileConfig_PartialFields(t *testing.T) {
	dir := t.TempDir()
	content := `
base_url = "https://example.com"
`
	os.WriteFile(filepath.Join(dir, ConfigFileName), []byte(content), 0o644)

	fc, err := LoadFileConfig(dir)
	if err != nil {
		t.Fatal(err)
	}
	if fc == nil {
		t.Fatal("expected non-nil config")
	}
	if *fc.BaseURL != "https://example.com" {
		t.Errorf("BaseURL = %q", *fc.BaseURL)
	}
	if fc.MainBranch != nil {
		t.Errorf("MainBranch should be nil, got %q", *fc.MainBranch)
	}
	if fc.OutputDir != nil {
		t.Errorf("OutputDir should be nil, got %q", *fc.OutputDir)
	}
}

func TestLoadFileConfig_NoFile(t *testing.T) {
	dir := t.TempDir()

	fc, err := LoadFileConfig(dir)
	if err != nil {
		t.Fatal(err)
	}
	if fc != nil {
		t.Error("expected nil config when no file exists")
	}
}

func TestLoadFileConfig_InvalidHCL(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, ConfigFileName), []byte(`{{{invalid`), 0o644)

	_, err := LoadFileConfig(dir)
	if err == nil {
		t.Error("expected error for invalid HCL")
	}
}

func TestLoadFileConfig_UnknownField(t *testing.T) {
	dir := t.TempDir()
	content := `
base_url    = "https://example.com"
unknown_key = "value"
`
	os.WriteFile(filepath.Join(dir, ConfigFileName), []byte(content), 0o644)

	_, err := LoadFileConfig(dir)
	if err == nil {
		t.Error("expected error for unknown field")
	}
}

func TestLoadFileConfig_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, ConfigFileName), []byte(""), 0o644)

	fc, err := LoadFileConfig(dir)
	if err != nil {
		t.Fatal(err)
	}
	if fc == nil {
		t.Fatal("expected non-nil config for empty file")
	}
	if fc.BaseURL != nil {
		t.Error("BaseURL should be nil")
	}
}
