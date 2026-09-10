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
	CollectionTitle string `json:"collection_title,omitzero"`
	CollectionId    int64  `json:"collection_id,omitzero"`
	Title           string `json:"title,omitzero"`
	Id              int64  `json:"id,omitzero"`
}

func (ctrl *Controller) getCollectionDescription(title string, id int64) (string, error) {
	if title == "" && id == 0 {
		return "", errors.New("either collection title or collection id must be provided")
	}

	// Try to resolve collection from ctrl.collections if id is provided or title is a numeric string
	if title != "" && id == 0 {
		if parsedID, err := strconv.ParseInt(strings.TrimSpace(title), 10, 64); err == nil && parsedID != 0 {
			id = parsedID
		}
	}

	if id != 0 {
		for _, coll := range ctrl.collections {
			if coll != nil && coll.Id == id {
				if title == "" || strings.TrimSpace(title) == strconv.FormatInt(id, 10) {
					title = coll.Title
				}
				break
			}
		}
	}

	if ctrl.markdowns == nil {
		return "", errors.New("no markdowns available")
	}

	var matchedMeta map[string]string

	// Direct lookup by standard key "collection.<title>"
	if title != "" {
		key := strings.ToLower(fmt.Sprintf("collection.%s", title))
		if meta, ok := ctrl.markdowns[key]; ok {
			matchedMeta = meta
		} else if meta, ok := ctrl.markdowns[strings.ToLower(title)]; ok {
			matchedMeta = meta
		}
	}

	// Search in all markdowns if not found yet
	if matchedMeta == nil {
		for _, meta := range ctrl.markdowns {
			if title != "" {
				if strings.EqualFold(meta["collectiontitle"], title) || strings.EqualFold(meta["title"], title) {
					matchedMeta = meta
					break
				}
			}
			if id != 0 {
				if meta["id"] == strconv.FormatInt(id, 10) || meta["collectionid"] == strconv.FormatInt(id, 10) {
					matchedMeta = meta
					break
				}
			}
		}
	}

	if matchedMeta == nil {
		if title != "" && id != 0 {
			return "", fmt.Errorf("collection description not found for title %q (id: %d)", title, id)
		} else if title != "" {
			return "", fmt.Errorf("collection description not found for title %q", title)
		}
		return "", fmt.Errorf("collection description not found for id %d", id)
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

	return "", fmt.Errorf("no description content found for collection %q", cmp.Or(title, matchedMeta["collectiontitle"], matchedMeta["title"]))
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
		return nil, ctrl.collections, nil
	})

	mcp.AddTool(mcpServer, &mcp.Tool{
		Name:        "get_collection_description",
		Description: "liefert die Beschreibung einer Sammlung anhand des Titels oder der ID",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args GetCollectionDescriptionArgs) (*mcp.CallToolResult, string, error) {
		title := cmp.Or(args.CollectionTitle, args.Title)
		id := cmp.Or(args.CollectionId, args.Id)
		desc, err := ctrl.getCollectionDescription(title, id)
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
