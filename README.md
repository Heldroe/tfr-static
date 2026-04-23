# tfr-static

A CLI tool for hosting a purely static [Terraform module registry](https://developer.hashicorp.com/terraform/internals/module-registry-protocol). It generates registry-protocol-compliant files from a git-based Terraform modules monorepo, ready to be uploaded to object storage (S3, GCS, etc.) and served behind a CDN.

## How it works

**Git tags are the source of truth.** Each module version is represented by a tag in the format `{module_path}-{semver}`:

```
hetzner/server-0.1.0
hetzner/server-1.0.0
aws/ec2/alb-0.0.1
aws/ec2/security-group-2.1.0
aws/iam/user-0.2.12
```

A module is any directory containing `*.tf` files (excluding the repository root). Modules can be nested — `aws/ec2` and `aws/ec2/alb` can both be independent modules.

For each published version, the following files are generated:

| File | Purpose |
|---|---|
| `{namespace}/{name}/{system}/{version}/module.tar.gz` | The module archive |
| `{namespace}/{name}/{system}/{version}/download` | HTML page with `<meta name="terraform-get">` pointing to the archive |
| `{namespace}/{name}/{system}/versions` | Version listing following the registry protocol |
| `.well-known/terraform.json` | [Service discovery](https://developer.hashicorp.com/terraform/internals/remote-service-discovery) document |

## Registry paths

The Terraform Module Registry Protocol requires all module paths to have exactly 3 segments: `namespace/name/system`. Directory paths are automatically mapped to registry paths using this convention:

| Part | Source | Example |
|---|---|---|
| **namespace** | Configurable (default `modules`) | `modules` |
| **name** | Directory segments after the first, joined with `-` | `ec2-security-group` |
| **system** | First directory segment (the provider) | `aws` |

Given this directory structure:

```
aws/ec2/instance        -> modules/ec2-instance/aws
aws/ec2/security-group  -> modules/ec2-security-group/aws
aws/ec2/eip/foo         -> modules/ec2-eip-foo/aws
aws/rds/db              -> modules/rds-db/aws
hetzner/server          -> modules/server/hetzner
```

**Ambiguity detection:** If two directory paths produce the same registry path (e.g. `aws/foo-bar/baz` and `aws/foo/bar-baz` both produce `modules/foo-bar-baz/aws`), the tool errors before publishing and suggests adding explicit mappings.

**Custom namespace:** Set via `--namespace`, `TFR_NAMESPACE`, or `namespace` in `.tfr-static.hcl`. Default: `modules`.

**Explicit overrides:** Add `module` blocks to `.tfr-static.hcl` to bypass auto-derivation:

```hcl
module "hetzner/server" {
  registry_path = "custom/server/hetzner"
}
```

**Important:** This only affects publishing — tags always use directory paths (e.g. `hetzner/server-1.0.0`), and all tagging logic remains unchanged.

## Usage with Terraform

Once the generated files are uploaded and served at a base URL, modules can be consumed using their **registry path** (3-segment):

```hcl
module "server" {
  source  = "registry.example.com/modules/server/hetzner"
  version = "1.0.0"
}
```

## Requirements

- Go 1.24+
- Git

## Build & install

```bash
# Build from source
go build .

# Or install directly
go install github.com/Heldroe/tfr-static@latest
```

## Configuration

Configuration is resolved with the following precedence: **CLI flags > environment variables > config file > defaults**.

### Config file

Place a `.tfr-static.hcl` file at the root of your modules repository:

```hcl
base_url                = "https://registry.example.com"
main_branch             = "main"
output_dir              = "target"
modules_path            = "/"
html                    = true
html_index              = "index.html"
gzip                    = true
terraform_docs          = true
invalidation_file       = "invalidation.json"
invalidation_format     = "cloudfront"
invalidation_full_url   = true
invalidation_base_url   = "https://cdn.example.com"
invalidation_url_encode = false
invalidation_dirs       = false
html_base               = "templates/base.html"
namespace               = "modules"

# Optional: override auto-derived registry paths for specific modules
module "hetzner/server" {
  registry_path = "custom/server/hetzner"
}
```

All fields are optional. Unknown fields will cause an error to catch typos early.

### CLI flags

| Flag | Description | Default |
|---|---|---|
| `--base-url` | Base URL for the registry | *(required for publish)* |
| `--main-branch` | Expected main branch for tagging | `main` |
| `--output-dir` | Output directory for generated files | `target` |
| `--modules-path` | Path prefix for `modules.v1` in service discovery | `/` |
| `--namespace` | Default namespace for auto-derived registry paths | `modules` |
| `--repo` | Path to the git repository | `.` |

### Environment variables

| Variable | Equivalent flag |
|---|---|
| `TFR_BASE_URL` | `--base-url` |
| `TFR_MAIN_BRANCH` | `--main-branch` |
| `TFR_OUTPUT_DIR` | `--output-dir` |
| `TFR_MODULES_PATH` | `--modules-path` |
| `TFR_NAMESPACE` | `--namespace` |
| `TFR_REPO_PATH` | `--repo` |
| `TFR_HTML` | `--html` |
| `TFR_HTML_INDEX` | `--html-index` |
| `TFR_GZIP` | `--gzip` |
| `TFR_TERRAFORM_DOCS` | `--terraform-docs` |
| `TFR_INVALIDATION_FILE` | `--invalidation-file` |
| `TFR_INVALIDATION_FORMAT` | `--invalidation-format` |
| `TFR_INVALIDATION_FULL_URL` | `--invalidation-full-url` |
| `TFR_INVALIDATION_BASE_URL` | `--invalidation-base-url` |
| `TFR_INVALIDATION_URL_ENCODE` | `--invalidation-url-encode` |
| `TFR_INVALIDATION_DIRS` | `--invalidation-dirs` |
| `TFR_HTML_BASE` | `--html-base` |
| `TFR_ADDR` | `--addr` (serve) |

## Commands

### `tfr-static publish`

Generates static registry files for module versions.

```bash
# Publish a specific tag (typical CI use case)
tfr-static publish --tag hetzner/server-1.0.0

# Publish from CI using an environment variable
TFR_TAG=hetzner/server-1.0.0 tfr-static publish

# Regenerate all versions of a specific module
tfr-static publish --module hetzner/server

# Regenerate everything (full rebuild)
tfr-static publish --all

# Publish from working tree as 0.0.0-dev (dev mode)
tfr-static publish --dev

# Publish a single module from working tree
tfr-static publish --dev --module hetzner/server

# Preview what would be generated
tfr-static publish --all --dry-run

# Publish and generate an invalidation file for CDN cache busting
tfr-static publish --tag hetzner/server-1.0.0 --invalidation-file invalidation.txt

# Generate a CloudFront-compatible invalidation batch
tfr-static publish --tag hetzner/server-1.0.0 \
  --invalidation-file invalidation.json \
  --invalidation-format cloudfront

# Publish with HTML documentation
tfr-static publish --all --html

# Publish with HTML documentation including terraform-docs output
tfr-static publish --all --html --terraform-docs

# Pre-compress text files for S3 upload
tfr-static publish --all --gzip
```

**Modes:**

| Flag | Behavior |
|---|---|
| `--tag` | Publish a single version. Generates the archive, download page, and an updated `versions`. |
| `--module` | Rebuild all versions of a module by iterating through its git tags. `versions` is generated once at the end. |
| `--all` | Rebuild all versions of all modules. |
| `--dev` | Publish modules from the current working tree as version `0.0.0-dev`. Compatible with `--module` to filter. Mutually exclusive with `--tag` and `--all`. |
| `--dry-run` | Show what would be generated and which paths would need CDN invalidation. |

| Flag | Description | Default |
|---|---|---|
| `--invalidation-file` | Write invalidation paths to this file | *(disabled)* |
| `--invalidation-format` | Format of the invalidation file: `txt`, `json`, `cloudfront` | `txt` |
| `--invalidation-full-url` | Prepend the base URL to invalidation paths | `false` |
| `--invalidation-base-url` | Override the base URL used for invalidation paths (requires `--invalidation-full-url`) | *(uses `--base-url`)* |
| `--invalidation-url-encode` | URL-encode the full invalidation paths (for use as query parameters) | `false` |
| `--invalidation-dirs` | Include directory paths (trailing `/`) for index files in invalidation output | `false` |
| `--html` | Generate HTML documentation pages for browsing modules | `false` |
| `--html-index` | Filename for HTML index pages | `index.html` |
| `--html-base` | Path to a custom base HTML template file | *(built-in default)* |
| `--terraform-docs` | Enrich HTML pages with auto-generated terraform-docs output (inputs, outputs, etc.) | `false` |
| `--gzip` | Gzip-compress text files for pre-compressed upload to S3 | `false` |

When using `--module` or `--all`, the tool iterates through git tag history. This means deleted modules (no longer in the current tree but still tagged) are still published correctly.

#### Invalidation file formats

The `--invalidation-file` flag writes all CDN paths that need cache invalidation after publishing. This is designed to plug into external tools like the AWS CLI — `tfr-static` itself does not manage uploads or invalidation.

**`txt`** (default) — one path per line:
```
/modules/server/hetzner/versions
/modules/server/hetzner/1.0.0/download
```

**`json`** — a JSON array of paths:
```json
[
  "/modules/server/hetzner/versions",
  "/modules/server/hetzner/1.0.0/download"
]
```

**`cloudfront`** — a JSON payload matching the AWS CloudFront [`create-invalidation --invalidation-batch`](https://docs.aws.amazon.com/cli/latest/reference/cloudfront/create-invalidation.html) schema:
```json
{
  "Paths": {
    "Quantity": 2,
    "Items": [
      "/modules/server/hetzner/versions",
      "/modules/server/hetzner/1.0.0/download"
    ]
  },
  "CallerReference": "tfr-static-1711296000"
}
```

Example CI usage with CloudFront:
```bash
tfr-static publish --tag "${GITHUB_REF_NAME}" \
  --invalidation-file invalidation.json \
  --invalidation-format cloudfront

aws cloudfront create-invalidation \
  --distribution-id "$DISTRIBUTION_ID" \
  --invalidation-batch file://invalidation.json
```

#### HTML documentation

The `--html` flag generates a browsable HTML documentation tree alongside the registry files:

```bash
tfr-static publish --all --html
```

This creates `index.html` pages at each level of the output:

```
target/
├── index.html                          (root: links to all modules)
├── modules/
│   └── server/
│       └── hetzner/
│           ├── index.html              (module: lists versions, shows README)
│           ├── 1.0.0/
│           │   └── index.html          (version: download link, shows README)
│           └── 0.1.0/
│               └── index.html
```

If a module directory contains a `README.md` next to its `.tf` files, the README is rendered as HTML on both the module page (from the latest version) and each version page (from that version's tag).

Use `--html-index` to change the filename (e.g. `--html-index docs.html`).

#### Custom base template

Use `--html-base` to provide your own HTML shell for all generated pages. The template must contain `{{.Title}}` and `{{.Content}}` placeholders:

```html
<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<title>{{.Title}}</title>
<!-- Add your own styles, favicons, analytics, etc. -->
</head>
<body>
{{.Content}}
</body>
</html>
```

The default template is available at [`internal/registry/templates/base.html`](internal/registry/templates/base.html) — copy it to your modules repository and customize it.

#### terraform-docs integration

The `--terraform-docs` flag automatically generates documentation for each module's inputs, outputs, providers, and resources using [terraform-docs](https://terraform-docs.io/) and injects it into the HTML pages.

```bash
tfr-static publish --all --html --terraform-docs
```

If a module's `README.md` contains `<!-- BEGIN_TF_DOCS -->` / `<!-- END_TF_DOCS -->` markers, the generated documentation replaces the content between them. Otherwise it is appended to the README.

A `.terraform-docs.yml` or `.terraform-docs.yaml` file can be used to customize the output format. The config file is searched for in the following locations (first match wins):

1. The module directory
2. The module directory's `.config/` subdirectory
3. The current working directory
4. The current working directory's `.config/` subdirectory
5. `$HOME/.tfdocs.d/`

#### Pre-compressed upload with `--gzip`

The `--gzip` flag gzip-compresses all text files (HTML, JSON, `download` pages) in the output directory. Archives (`.tar.gz`) are left untouched since they are already compressed.

Files are compressed in-place and keep their original names — no `.gz` extension is added. This is designed for uploading pre-compressed files to S3 with the appropriate `Content-Encoding` header.

```bash
tfr-static publish --all --gzip
```

When uploading to S3, text files and archives need different headers. Use two `aws s3 cp` commands:

```bash
# Text files: Content-Encoding: gzip (browsers decompress transparently)
aws s3 cp target/ s3://your-registry-bucket/ --recursive \
  --exclude "*.tar.gz" \
  --content-encoding gzip

# Archives: no Content-Encoding (gzip IS the content format)
aws s3 cp target/ s3://your-registry-bucket/ --recursive \
  --exclude "*" --include "*.tar.gz" \
  --content-type application/gzip
```

Without `--gzip`, a single `aws s3 cp --recursive` is sufficient.

### `tfr-static tag`

Interactive helper for creating correctly formatted version tags.

```bash
# Interactive: select module from a filterable list, then pick version bump
tfr-static tag

# Tag a specific module
tfr-static tag hetzner/server

# Only show modules with changes since their latest tag
tfr-static tag --pending
```

| Flag | Description | Default |
|---|---|---|
| `--pending` | Only show modules with changes since their latest tag | `false` |

The tag command:

1. Verifies you're on the main branch and up to date with the remote
2. Shows a filterable module selector (type to search, arrows to navigate) if no module is specified
3. Finds the latest existing version from git tags
4. Presents version bump options with the resulting version number:
   ```
   Patch release => 1.9.2  (small fixes, no resource or variables changes)
   Minor release => 1.10.0 (variables changes, added or removed resources)
   Major release => 2.0.0  (breaking changes, state modification required)
   ```
5. Creates the annotated git tag
6. Optionally pushes the tag to the remote

For new modules with no existing tags, bumping starts from `0.0.0`.

The `--pending` flag filters the module list to only those with changes on the main branch since their latest tag. Modules with no tags at all are always included (they need their first version). This is useful for identifying which modules have unreleased work.

### `tfr-static serve`

Start a local HTTP server for the registry.

```bash
# Serve the generated static files
tfr-static serve

# Serve on a custom address
tfr-static serve --addr localhost:9090

# Dev mode: serve current working tree for all version requests
tfr-static serve --dev
```

**Static mode** (default) serves the output directory as-is, acting as if it were the remote CDN or object storage.

**Dev mode** (`--dev`) is designed for local development. It dynamically serves modules from the current working tree, including uncommitted changes. Every version request returns the current code, regardless of which version was asked for. HTML documentation pages are always enabled in dev mode. This lets you:

1. Point Terraform at `localhost:8080` instead of your production registry
2. Keep your existing `version = "1.0.0"` constraints unchanged
3. See your local changes applied immediately without tagging or publishing

In dev mode:
- `/.well-known/terraform.json` returns the service discovery document
- `/{namespace}/{name}/{system}/versions` returns all real tagged versions plus synthetic dev versions (`0.0.0-dev` and `99999.0.0-dev`) so that any version constraint can match
- `/{namespace}/{name}/{system}/{version}/download` always points to an on-the-fly archive regardless of the requested version
- Archives are built from the filesystem (not from git), so uncommitted changes are included
- Browsable HTML pages are served at `/`, `/{namespace}/{name}/{system}/`, and `/{namespace}/{name}/{system}/{version}/` with README rendering
- Directory paths are automatically mapped to 3-segment registry paths (see [Registry paths](#registry-paths))

| Flag | Description | Default |
|---|---|---|
| `--addr` | Address to listen on | `localhost:8080` |
| `--dev` | Enable dev mode | `false` |

## Generated output structure

Output directories use 3-segment registry paths (see [Registry paths](#registry-paths)):

```
target/
├── .well-known/
│   └── terraform.json
└── modules/
    └── server/
        └── hetzner/
            ├── versions
            ├── 0.1.0/
            │   ├── download
            │   └── module.tar.gz
            └── 1.0.0/
                ├── download
                └── module.tar.gz
```

The `.well-known/terraform.json` file implements [Terraform service discovery](https://developer.hashicorp.com/terraform/internals/remote-service-discovery), telling Terraform where the module API lives:

```json
{
  "modules.v1": "/"
}
```

If your modules are served from a subpath (e.g. behind a reverse proxy at `/v1/modules/`), set `modules_path` accordingly in your config or via `--modules-path`.

The `download` file is an HTML page that Terraform uses for module source resolution:

```html
<meta name="terraform-get" content="https://registry.example.com/modules/server/hetzner/1.0.0/module.tar.gz" />
```

The `versions` file follows the [module versions protocol](https://developer.hashicorp.com/terraform/internals/module-registry-protocol#list-available-versions-for-a-module):

```json
{
  "modules": [
    {
      "versions": [
        {"version": "1.0.0"},
        {"version": "0.1.0"}
      ]
    }
  ]
}
```

## CI integration

### GitHub Actions (tag-triggered publish)

```yaml
name: Publish module
on:
  push:
    tags:
      - '**-[0-9]*.[0-9]*.[0-9]*'

jobs:
  publish:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
        with:
          fetch-depth: 0  # needed for tag history

      - uses: actions/setup-go@v5
        with:
          go-version: '1.24'

      - run: go install github.com/Heldroe/tfr-static@latest

      - run: |
          tfr-static publish --tag "${GITHUB_REF_NAME}" \
            --invalidation-file invalidation.json \
            --invalidation-format cloudfront
        env:
          TFR_BASE_URL: https://registry.example.com

      - name: Upload to S3
        run: aws s3 sync target/ s3://your-registry-bucket/ --delete

      - name: Invalidate CloudFront cache
        run: |
          aws cloudfront create-invalidation \
            --distribution-id "$DISTRIBUTION_ID" \
            --invalidation-batch file://invalidation.json
```

### Full rebuild

```yaml
- run: tfr-static publish --all
  env:
    TFR_BASE_URL: https://registry.example.com
```

## Tag format

Tags follow the pattern `{module_path}-{semver}`. The module path can contain slashes and dashes:

| Tag | Module path | Version |
|---|---|---|
| `hetzner/server-1.0.0` | `hetzner/server` | `1.0.0` |
| `aws/ec2/security-group-0.0.1` | `aws/ec2/security-group` | `0.0.1` |
| `my-org/my-module-2.1.0` | `my-org/my-module` | `2.1.0` |

Parsing is unambiguous: the tag is scanned from right to left for a `-` followed by a valid [strict semver](https://semver.org/). The `v` prefix (e.g. `v1.0.0`) is **not** supported.

## Repository layout

```
my-terraform-modules/
├── .tfr-static.hcl          # optional config
├── hetzner/
│   ├── server/
│   │   ├── main.tf          # module: hetzner/server
│   │   └── variables.tf
│   └── network/
│       └── main.tf          # module: hetzner/network
├── aws/
│   ├── ec2/
│   │   ├── main.tf          # module: aws/ec2 (parent is also a module)
│   │   ├── alb/
│   │   │   └── main.tf      # module: aws/ec2/alb
│   │   └── security-group/
│   │       └── main.tf      # module: aws/ec2/security-group
│   └── iam/
│       └── user/
│           └── main.tf      # module: aws/iam/user
└── root.tf                   # ignored (root directory is excluded)
```

A directory is a module if it contains at least one `*.tf` file. The root directory is excluded. Hidden directories (`.git`, `.terraform`, etc.) are skipped.

## Running tests

```bash
go test ./...
```

Tests cover tag parsing (including edge cases with dashes, pre-release versions, and malformed tags), module discovery, version ordering, archive generation from git history (including deleted modules), registry file generation, and config file loading.
