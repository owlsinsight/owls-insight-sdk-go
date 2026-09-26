package owls

// The models in models_gen.go are generated from spec/openapi.json (vendored from
// owls-insight-sdk-js) with the Go overlay in spec/go-overlay.yaml, then their doc
// comments are tidied (internal/cmd/doctidy). The generator is pinned here and is
// not a module dependency.
//go:generate go run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@v2.8.0 -config oapi-codegen.yaml spec/openapi.json
//go:generate go run ./internal/cmd/doctidy models_gen.go
