package server

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"net/url"
	"reflect"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type GetCollectionDescriptionArgs struct {
	Title string `json:"title,omitzero"`
	Id    int64  `json:"id,omitzero"`
}

type GetCollectionsResult struct {
	Collections []*CollFacetType `json:"collections"`
}

type GetCollectionDescriptionResult struct {
	Description string `json:"description"`
}

type GetEstateDescriptionArgs struct {
	Title string `json:"title,omitzero"`
	Id    int64  `json:"id,omitzero"`
}

type GetEstatesResult struct {
	Estates []*CollFacetType `json:"estates"`
}

type GetEstateDescriptionResult struct {
	Description string `json:"description"`
}

func (ctrl *Controller) getItemDescription(itemType string, items []*CollFacetType, title string, id int64) (string, error) {
	if title == "" && id == 0 {
		return "", fmt.Errorf("either %s title or %s id must be provided", itemType, itemType)
	}

	// Try to resolve item from items if id is provided or title is a numeric string
	if title != "" && id == 0 {
		if parsedID, err := strconv.ParseInt(strings.TrimSpace(title), 10, 64); err == nil && parsedID != 0 {
			id = parsedID
		}
	}

	if id != 0 {
		for _, item := range items {
			if item != nil && item.Id == id {
				if title == "" || strings.TrimSpace(title) == strconv.FormatInt(id, 10) {
					title = item.Title
				}
				break
			}
		}
	}

	if ctrl.markdowns == nil {
		return "", errors.New("no markdowns available")
	}

	var matchedMeta map[string]string

	// Direct lookup by standard key "<itemType>.<title>"
	if title != "" {
		key := strings.ToLower(fmt.Sprintf("%s.%s", itemType, title))
		if meta, ok := ctrl.markdowns[key]; ok {
			matchedMeta = meta
		} else if meta, ok := ctrl.markdowns[strings.ToLower(title)]; ok {
			matchedMeta = meta
		}
	}

	// Search in all markdowns if not found yet
	if matchedMeta == nil {
		typeTitleKey := itemType + "title"
		typeIdKey := itemType + "id"
		for _, meta := range ctrl.markdowns {
			if title != "" {
				if strings.EqualFold(meta[typeTitleKey], title) || strings.EqualFold(meta["title"], title) {
					matchedMeta = meta
					break
				}
			}
			if id != 0 {
				if meta["id"] == strconv.FormatInt(id, 10) || meta[typeIdKey] == strconv.FormatInt(id, 10) {
					matchedMeta = meta
					break
				}
			}
		}
	}

	if matchedMeta == nil {
		if title != "" && id != 0 {
			return "", fmt.Errorf("%s description not found for title %q (id: %d)", itemType, title, id)
		} else if title != "" {
			return "", fmt.Errorf("%s description not found for title %q", itemType, title)
		}
		return "", fmt.Errorf("%s description not found for id %d", itemType, id)
	}

	// Read markdown file from pagesFS if available
	pathName := matchedMeta["path"]
	if pathName != "" && ctrl.pagesFS != nil {
		mdData, err := fs.ReadFile(ctrl.pagesFS, pathName)
		if err == nil {
			_, body := ctrl.parseMarkdown(mdData, pathName)
			if body != "" {
				return body, nil
			}
		}
	}

	// Fallback to meta fields if present
	for _, field := range []string{"description", "abstract", "markdown", "content", "body", "text"} {
		if val, ok := matchedMeta[field]; ok && val != "" {
			return val, nil
		}
	}

	return "", fmt.Errorf("no description content found for %s %q", itemType, cmp.Or(title, matchedMeta[itemType+"title"], matchedMeta["title"]))
}

func (ctrl *Controller) getCollectionDescription(title string, id int64) (string, error) {
	return ctrl.getItemDescription("collection", ctrl.getCollections(), title, id)
}

func (ctrl *Controller) getEstateDescription(title string, id int64) (string, error) {
	return ctrl.getItemDescription("estate", ctrl.getEstates(), title, id)
}

func normalizeSchemaNode(node any) any {
	switch v := node.(type) {
	case map[string]any:
		result := make(map[string]any, len(v))
		for k, val := range v {
			if k == "type" {
				if typeArr, ok := val.([]any); ok {
					if len(typeArr) == 1 {
						if strType, isStr := typeArr[0].(string); isStr {
							result["type"] = strType
						} else {
							result["type"] = typeArr[0]
						}
					} else if len(typeArr) > 1 {
						anyOfList := make([]any, 0, len(typeArr))
						for _, t := range typeArr {
							anyOfList = append(anyOfList, map[string]any{
								"type": t,
							})
						}
						result["anyOf"] = anyOfList
					} else {
						result["type"] = val
					}
				} else {
					result["type"] = normalizeSchemaNode(val)
				}
			} else {
				result[k] = normalizeSchemaNode(val)
			}
		}
		return result
	case []any:
		result := make([]any, len(v))
		for i, item := range v {
			result[i] = normalizeSchemaNode(item)
		}
		return result
	default:
		return node
	}
}

func normalizeSchema(schema any) any {
	if schema == nil {
		return nil
	}
	raw, err := json.Marshal(schema)
	if err != nil {
		return schema
	}
	var data any
	if err := json.Unmarshal(raw, &data); err != nil {
		return schema
	}
	return normalizeSchemaNode(data)
}

func schemaFor[T any]() any {
	rt := reflect.TypeFor[T]()
	if rt.Kind() == reflect.Pointer {
		rt = rt.Elem()
	}
	s, err := jsonschema.ForType(rt, &jsonschema.ForOptions{})
	if err != nil {
		return nil
	}
	return s
}

func addTool[In, Out any](s *mcp.Server, tool *mcp.Tool, handler func(context.Context, *mcp.CallToolRequest, In) (*mcp.CallToolResult, Out, error)) {
	if tool.InputSchema == nil {
		if reflect.TypeFor[In]() == reflect.TypeFor[any]() {
			tool.InputSchema = map[string]any{"type": "object"}
		} else {
			tool.InputSchema = normalizeSchema(schemaFor[In]())
		}
	} else {
		tool.InputSchema = normalizeSchema(tool.InputSchema)
	}

	if tool.OutputSchema == nil {
		if reflect.TypeFor[Out]() != reflect.TypeFor[any]() {
			tool.OutputSchema = normalizeSchema(schemaFor[Out]())
		}
	} else {
		tool.OutputSchema = normalizeSchema(tool.OutputSchema)
	}

	mcp.AddTool(s, tool, handler)
}

func (ctrl *Controller) initMCP(router *gin.Engine) {
	mcpRouter := router.Group("/mcp")
	mcpServer := mcp.NewServer(&mcp.Implementation{
		Name:    fmt.Sprintf("%s MCP Server", ctrl.name),
		Version: "0.0.1",
	}, nil)

	// Resource Templates für Sammlungen und Nachlässe registrieren
	mcpServer.AddResourceTemplate(&mcp.ResourceTemplate{
		URITemplate: "collection://{title}",
		Name:        "collection_description",
		Title:       "Sammlung Markdown-Beschreibung",
		Description: "Liefert die Markdown-Beschreibung einer Sammlung anhand des Titels",
		MIMEType:    "text/markdown",
	}, func(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		rawTitle := strings.TrimPrefix(req.Params.URI, "collection://")
		title, err := url.PathUnescape(rawTitle)
		if err != nil {
			title = rawTitle
		}
		desc, err := ctrl.getCollectionDescription(title, 0)
		if err != nil {
			return nil, mcp.ResourceNotFoundError(req.Params.URI)
		}
		return &mcp.ReadResourceResult{
			Contents: []*mcp.ResourceContents{
				{
					URI:      req.Params.URI,
					MIMEType: "text/markdown",
					Text:     desc,
				},
			},
		}, nil
	})

	mcpServer.AddResourceTemplate(&mcp.ResourceTemplate{
		URITemplate: "estate://{title}",
		Name:        "estate_description",
		Title:       "Bestand Markdown-Beschreibung",
		Description: "Liefert die Markdown-Beschreibung eines Bestandes anhand des Titels",
		MIMEType:    "text/markdown",
	}, func(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		rawTitle := strings.TrimPrefix(req.Params.URI, "estate://")
		title, err := url.PathUnescape(rawTitle)
		if err != nil {
			title = rawTitle
		}
		desc, err := ctrl.getEstateDescription(title, 0)
		if err != nil {
			return nil, mcp.ResourceNotFoundError(req.Params.URI)
		}
		return &mcp.ReadResourceResult{
			Contents: []*mcp.ResourceContents{
				{
					URI:      req.Params.URI,
					MIMEType: "text/markdown",
					Text:     desc,
				},
			},
		}, nil
	})

	// Tools wie gewohnt registrieren
	addTool(mcpServer, &mcp.Tool{
		Name:        "get_collections",
		Description: "liefert eine Liste der Sammlungen",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args struct{}) (*mcp.CallToolResult, *GetCollectionsResult, error) {
		return nil, &GetCollectionsResult{Collections: ctrl.getCollections()}, nil
	})

	addTool(mcpServer, &mcp.Tool{
		Name:        "get_collection_description",
		Description: "liefert die Beschreibung einer Sammlung anhand des Titels oder der ID als formatierter Markdown-Text",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args GetCollectionDescriptionArgs) (*mcp.CallToolResult, *GetCollectionDescriptionResult, error) {
		desc, err := ctrl.getCollectionDescription(args.Title, args.Id)
		if err != nil {
			return nil, nil, err
		}
		itemRef := args.Title
		if itemRef == "" && args.Id != 0 {
			itemRef = strconv.FormatInt(args.Id, 10)
		}
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.EmbeddedResource{
					Resource: &mcp.ResourceContents{
						URI:      fmt.Sprintf("collection://%s", itemRef),
						MIMEType: "text/markdown",
						Text:     desc,
					},
				},
			},
		}, &GetCollectionDescriptionResult{Description: desc}, nil
	})

	addTool(mcpServer, &mcp.Tool{
		Name:        "get_estates",
		Description: "liefert eine Liste der Nachlässe",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args struct{}) (*mcp.CallToolResult, *GetEstatesResult, error) {
		return nil, &GetEstatesResult{Estates: ctrl.getEstates()}, nil
	})

	addTool(mcpServer, &mcp.Tool{
		Name:        "get_estate_description",
		Description: "liefert die Beschreibung eines Bestands anhand des Titels oder der ID als formatierten Markdown-Text",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args GetEstateDescriptionArgs) (*mcp.CallToolResult, *GetEstateDescriptionResult, error) {
		desc, err := ctrl.getEstateDescription(args.Title, args.Id)
		if err != nil {
			return nil, nil, err
		}
		itemRef := args.Title
		if itemRef == "" && args.Id != 0 {
			itemRef = strconv.FormatInt(args.Id, 10)
		}
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.EmbeddedResource{
					Resource: &mcp.ResourceContents{
						URI:      fmt.Sprintf("estate://%s", itemRef),
						MIMEType: "text/markdown",
						Text:     desc,
					},
				},
			},
		}, &GetEstateDescriptionResult{Description: desc}, nil
	})

	streamableHandler := mcp.NewStreamableHTTPHandler(func(r *http.Request) *mcp.Server {
		return mcpServer
	}, nil)
	mcpRouter.Any("/*any", gin.WrapH(streamableHandler))
}
