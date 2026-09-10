package server

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type GetCollectionDescriptionArgs struct {
	Title string `json:"title,omitzero"`
	Id    int64  `json:"id,omitzero"`
}

type GetEstateDescriptionArgs struct {
	Title string `json:"title,omitzero"`
	Id    int64  `json:"id,omitzero"`
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

func (ctrl *Controller) initMCP(router *gin.Engine) {
	mcpRouter := router.Group("/mcp")
	mcpServer := mcp.NewServer(&mcp.Implementation{
		Name:    fmt.Sprintf("%s MCP Server", ctrl.name),
		Version: "0.0.1",
	}, nil)

	// Tools wie gewohnt registrieren
	//mcpServer.AddTools(tool)
	mcp.AddTool(mcpServer, &mcp.Tool{
		Name:        "get_collections",
		Description: "liefert eine Liste der Sammlungen",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args struct{}) (*mcp.CallToolResult, []*CollFacetType, error) {
		return nil, ctrl.getCollections(), nil
	})

	mcp.AddTool(mcpServer, &mcp.Tool{
		Name:        "get_collection_description",
		Description: "liefert die Beschreibung einer Sammlung anhand des Titels oder der ID",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args GetCollectionDescriptionArgs) (*mcp.CallToolResult, string, error) {
		desc, err := ctrl.getCollectionDescription(args.Title, args.Id)
		if err != nil {
			return nil, "", err
		}
		return nil, desc, nil
	})

	mcp.AddTool(mcpServer, &mcp.Tool{
		Name:        "get_estates",
		Description: "liefert eine Liste der Nachlässe",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args struct{}) (*mcp.CallToolResult, []*CollFacetType, error) {
		return nil, ctrl.getEstates(), nil
	})

	mcp.AddTool(mcpServer, &mcp.Tool{
		Name:        "get_estate_description",
		Description: "liefert die Bestandes eines Nachlasses anhand des Titels oder der ID",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args GetEstateDescriptionArgs) (*mcp.CallToolResult, string, error) {
		desc, err := ctrl.getEstateDescription(args.Title, args.Id)
		if err != nil {
			return nil, "", err
		}
		return nil, desc, nil
	})

	sseHandler := mcp.NewSSEHandler(func(r *http.Request) *mcp.Server {
		return mcpServer
	}, nil)
	mcpRouter.Any("/*any", gin.WrapH(sseHandler))
}
