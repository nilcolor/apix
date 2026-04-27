package loader

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"

	"github.com/nilcolor/apix/internal/schema"
)

// Load reads the YAML file at path, resolves all includes recursively, merges
// config, and tags each step's Origin field.
func Load(path string) (*schema.RequestFile, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("loader: resolve path %q: %w", path, err)
	}
	return load(abs, nil)
}

// load is the recursive worker. stack tracks ancestor paths for cycle detection.
func load(abs string, stack []string) (*schema.RequestFile, error) {
	for _, ancestor := range stack {
		if ancestor == abs {
			return nil, fmt.Errorf("loader: circular include detected: %v -> %s", stack, abs)
		}
	}

	data, err := os.ReadFile(abs) //nolint:gosec // reading user-specified files is the core purpose of this CLI
	if err != nil {
		return nil, fmt.Errorf("loader: read %q: %w", abs, err)
	}

	var f schema.RequestFile
	if err := yaml.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("loader: parse %q: %w", abs, err)
	}

	// Tag steps from this file as "current" initially; included steps will be
	// re-tagged as "included" by the caller.
	for i := range f.Steps {
		f.Steps[i].Origin = "current"
	}

	if len(f.Include) == 0 {
		return &f, nil
	}

	// Process includes in order, building merged result bottom-up.
	// Each include's steps run before the current file's steps.
	merged := &schema.RequestFile{
		Config:    schema.Config{},
		Variables: map[string]string{},
	}

	dir := filepath.Dir(abs)
	newStack := append(stack, abs) //nolint:gocritic // intentional new slice per recursion level, not re-assigning stack

	for _, inc := range f.Include {
		incPath := inc
		if !filepath.IsAbs(incPath) {
			incPath = filepath.Join(dir, inc)
		}
		incPath, err = filepath.Abs(incPath)
		if err != nil {
			return nil, fmt.Errorf("loader: resolve include %q in %q: %w", inc, abs, err)
		}

		incFile, err := load(incPath, newStack)
		if err != nil {
			return nil, err
		}

		// Tag all steps from includes as "included".
		for i := range incFile.Steps {
			incFile.Steps[i].Origin = "included"
		}

		// Merge config: included file provides base; current merged wins per field.
		merged.Config = mergeConfig(incFile.Config, merged.Config)

		// Merge variables: later includes win per key (will be overridden by current file next).
		for k, v := range incFile.Variables {
			merged.Variables[k] = v
		}

		// Prepend steps.
		merged.Steps = append(merged.Steps, incFile.Steps...)
	}

	// Current file's config overrides the merged included config.
	merged.Config = mergeConfig(merged.Config, f.Config)

	// Current file's variables override included variables.
	for k, v := range f.Variables {
		merged.Variables[k] = v
	}

	// Current file's steps come after all included steps.
	merged.Steps = append(merged.Steps, f.Steps...)
	merged.Include = f.Include

	return merged, nil
}

// mergeConfig merges src (lower priority) into dst (higher priority).
// Non-zero / non-nil fields in dst win; absent fields fall back to src.
// Headers are merged key-wise: dst wins per key.
func mergeConfig(src, dst schema.Config) schema.Config {
	result := src

	if dst.BaseURL != "" {
		result.BaseURL = dst.BaseURL
	}
	if dst.Timeout.Duration != 0 {
		result.Timeout = dst.Timeout
	}
	if dst.FollowRedirects != nil {
		result.FollowRedirects = dst.FollowRedirects
	}
	if dst.TLSVerify != nil {
		result.TLSVerify = dst.TLSVerify
	}

	// Merge headers: start from src, let dst keys win.
	if len(src.Headers) > 0 || len(dst.Headers) > 0 {
		merged := make(map[string]string, len(src.Headers)+len(dst.Headers))
		for k, v := range src.Headers {
			merged[k] = v
		}
		for k, v := range dst.Headers {
			merged[k] = v
		}
		result.Headers = merged
	}

	return result
}
