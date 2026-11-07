package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"path"
	"reflect"
	"slices"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/kunalgoesbyken/seedfall"
	"github.com/kunalgoesbyken/seedfall/engine"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const configSchemaURI = "seedfall://schema/config"

type markdownDoc struct {
	Path, Name, Title, Summary, Body string
	Meta                             map[string]string
}

func readEmbedded(pattern string) ([]markdownDoc, error) {
	paths, err := fs.Glob(seedfall.Files, pattern)
	if err != nil {
		return nil, err
	}
	docs := make([]markdownDoc, 0, len(paths))
	for _, p := range paths {
		b, err := seedfall.Files.ReadFile(p)
		if err != nil {
			return nil, err
		}
		d := parseMarkdown(strings.ReplaceAll(string(b), "\r\n", "\n"))
		d.Path, d.Name = p, strings.TrimSuffix(path.Base(p), ".md")
		docs = append(docs, d)
	}
	return docs, nil
}

func parseMarkdown(text string) markdownDoc {
	d := markdownDoc{Body: text, Meta: map[string]string{}}
	if rest, ok := strings.CutPrefix(text, "---\n"); ok {
		if front, body, ok := strings.Cut(rest, "\n---\n"); ok {
			for _, line := range strings.Split(front, "\n") {
				if k, v, ok := strings.Cut(line, ":"); ok {
					d.Meta[strings.TrimSpace(k)] = strings.TrimSpace(v)
				}
			}
			d.Body = strings.TrimLeft(body, "\n")
		}
	}
	title, rest, _ := strings.Cut(strings.TrimSpace(d.Body), "\n\n")
	d.Title = strings.TrimPrefix(title, "# ")
	para, _, _ := strings.Cut(strings.TrimSpace(rest), "\n\n")
	if !slices.ContainsFunc([]string{"#", "```", "|", "<"}, func(prefix string) bool { return strings.HasPrefix(para, prefix) }) {
		d.Summary = strings.Join(strings.Fields(para), " ")
	}
	return d
}

func textResource(uri, mime, text string) mcp.ResourceHandler {
	return func(context.Context, *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{URI: uri, MIMEType: mime, Text: text}}}, nil
	}
}

func addResources(server *mcp.Server) error {
	docs, err := readEmbedded("docs/*.md")
	if err != nil {
		return err
	}
	for _, d := range docs {
		uri := "seedfall://docs/" + d.Name
		server.AddResource(&mcp.Resource{URI: uri, Name: d.Name, Title: d.Title, Description: d.Summary, MIMEType: "text/markdown"}, textResource(uri, "text/markdown", d.Body))
	}
	schema, err := configSchema()
	if err != nil {
		return err
	}
	server.AddResource(&mcp.Resource{
		URI: configSchemaURI, Name: "config-schema", Title: "seedfall.json schema", MIMEType: "application/schema+json",
		Description: "JSON Schema of seedfall.json with every field's default. Field meanings are in seedfall://docs/faults.",
	}, textResource(configSchemaURI, "application/schema+json", schema))
	return nil
}

func addPrompts(server *mcp.Server) error {
	skills, err := readEmbedded("skills/*/SKILL.md")
	if err != nil {
		return err
	}
	for _, s := range skills {
		name, description, body := s.Meta["name"], s.Meta["description"], s.Body
		if name == "" || description == "" {
			return fmt.Errorf("%s: frontmatter needs name and description", s.Path)
		}
		server.AddPrompt(&mcp.Prompt{Name: name, Title: s.Title, Description: description}, func(context.Context, *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
			return &mcp.GetPromptResult{Description: description, Messages: []*mcp.PromptMessage{{Role: "user", Content: &mcp.TextContent{Text: body}}}}, nil
		})
	}
	return nil
}

func schemaOptions() *jsonschema.ForOptions {
	duration := &jsonschema.Schema{Type: "string", Description: "Go duration, for example 250ms or 2s"}
	two := 2
	return &jsonschema.ForOptions{TypeSchemas: map[reflect.Type]*jsonschema.Schema{
		reflect.TypeFor[engine.Duration](): duration,
		reflect.TypeFor[engine.Range]():    {Type: "array", Items: duration, MinItems: &two, MaxItems: &two, Description: "inclusive range of two durations"},
	}}
}

func configSchema() (string, error) {
	s, err := jsonschema.For[engine.Config](schemaOptions())
	if err != nil {
		return "", err
	}
	b, err := json.Marshal(engine.DefaultConfig())
	if err != nil {
		return "", err
	}
	var defaults map[string]any
	if err := json.Unmarshal(b, &defaults); err != nil {
		return "", err
	}
	if err := applyDefaults(s, defaults); err != nil {
		return "", err
	}
	s.Required, s.Properties["faults"].Required = []string{"workload"}, nil
	s.Title = "seedfall.json"
	out, err := json.MarshalIndent(s, "", "  ")
	return string(out), err
}

func applyDefaults(s *jsonschema.Schema, values map[string]any) error {
	for name, prop := range s.Properties {
		v := values[name]
		if nested, ok := v.(map[string]any); ok {
			if err := applyDefaults(prop, nested); err != nil {
				return err
			}
			continue
		}
		if v == nil || v == "" {
			continue
		}
		b, err := json.Marshal(v)
		if err != nil {
			return err
		}
		prop.Default = b
	}
	return nil
}
