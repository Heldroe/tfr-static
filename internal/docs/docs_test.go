package docs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGenerate_BasicModule(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "main.tf"), []byte(`
variable "name" {
  description = "The name of the resource"
  type        = string
}

output "id" {
  description = "The resource ID"
  value       = "test"
}
`), 0o644)

	result, err := Generate(dir)
	if err != nil {
		t.Fatal(err)
	}

	if result == "" {
		t.Fatal("expected non-empty output")
	}
	if !strings.Contains(result, "name") {
		t.Error("output should mention the variable name")
	}
	if !strings.Contains(result, "id") {
		t.Error("output should mention the output id")
	}
}

func TestGenerate_NoTFFiles(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "readme.txt"), []byte("hello"), 0o644)

	result, err := Generate(dir)
	if err != nil {
		t.Fatal(err)
	}
	if result != "" {
		t.Errorf("expected empty result for dir without .tf files, got: %s", result)
	}
}

func TestInjectIntoReadme_WithMarkers(t *testing.T) {
	readme := `# My Module

Some description.

<!-- BEGIN_TF_DOCS -->
old content
<!-- END_TF_DOCS -->

## Footer
`
	docs := "| Name | Description |\n|------|-------------|"

	result := InjectIntoReadme(readme, docs)

	if !strings.Contains(result, "## Footer") {
		t.Error("footer should be preserved")
	}
	if !strings.Contains(result, docs) {
		t.Error("docs should be injected")
	}
	if strings.Contains(result, "old content") {
		t.Error("old content should be replaced")
	}
}

func TestInjectIntoReadme_WithoutMarkers(t *testing.T) {
	readme := "# My Module\n\nSome description."
	docs := "## Inputs\n| Name |"

	result := InjectIntoReadme(readme, docs)

	if !strings.Contains(result, "# My Module") {
		t.Error("original content should be preserved")
	}
	if !strings.Contains(result, docs) {
		t.Error("docs should be appended")
	}
}

func TestInjectIntoReadme_EmptyReadme(t *testing.T) {
	result := InjectIntoReadme("", "some docs")
	if result != "some docs" {
		t.Errorf("expected 'some docs', got %q", result)
	}
}

func TestInjectIntoReadme_EmptyDocs(t *testing.T) {
	readme := "# Hello"
	result := InjectIntoReadme(readme, "")
	if result != readme {
		t.Errorf("expected unchanged readme, got %q", result)
	}
}
