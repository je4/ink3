package server

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
	"testing/fstest"

	"github.com/gin-gonic/gin"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestGetCollectionsTool(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()

	collections := []*CollFacetType{
		{
			Id:         1,
			Title:      "Test Collection 1",
			Identifier: "cat:\"test1\"",
			Url:        "https://example.com/1",
		},
		{
			Id:         2,
			Title:      "Test Collection 2",
			Identifier: "cat:\"test2\"",
			Url:        "https://example.com/2",
		},
	}

	ctrl := &Controller{
		name:        "test",
		collections: collections,
	}

	ctrl.initMCP(router)

	ts := httptest.NewServer(router)
	defer ts.Close()

	clientTransport := &mcp.SSEClientTransport{
		Endpoint: ts.URL + "/mcp",
	}
	client := mcp.NewClient(&mcp.Implementation{
		Name:    "test client",
		Version: "0.0.1",
	}, nil)

	session, err := client.Connect(t.Context(), clientTransport, nil)
	if err != nil {
		t.Fatalf("failed to connect via SSE: %v", err)
	}
	defer session.Close()

	toolsList, err := session.ListTools(t.Context(), &mcp.ListToolsParams{})
	if err != nil {
		t.Fatalf("failed to list tools: %v", err)
	}

	var found bool
	for _, tool := range toolsList.Tools {
		if tool.Name == "get_collections" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("tool 'get_collections' not found in tools list")
	}

	res, err := session.CallTool(t.Context(), &mcp.CallToolParams{
		Name: "get_collections",
	})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}

	if res.IsError {
		t.Fatalf("CallTool returned error")
	}

	raw, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatalf("failed to marshal structured content: %v", err)
	}
	var resultColls []*CollFacetType
	if err := json.Unmarshal(raw, &resultColls); err != nil {
		t.Fatalf("failed to unmarshal structured content: %v", err)
	}

	if len(resultColls) != 2 {
		t.Fatalf("expected 2 collections, got %d", len(resultColls))
	}
	if resultColls[0].Title != "Test Collection 1" {
		t.Errorf("expected Title 'Test Collection 1', got '%s'", resultColls[0].Title)
	}
	if resultColls[1].Identifier != "cat:\"test2\"" {
		t.Errorf("expected Identifier 'cat:\"test2\"', got '%s'", resultColls[1].Identifier)
	}
}

func TestGetCollectionDescriptionTool(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()

	pagesFS := fstest.MapFS{
		"pages/coll1.md": &fstest.MapFile{
			Data: []byte(`---
type: collection
collectiontitle: Test Collection 1
title: Test Collection 1
---
Dies ist die Beschreibung der ersten Sammlung.`),
		},
		"pages/coll2.md": &fstest.MapFile{
			Data: []byte(`---
type: collection
collectiontitle: Test Collection 2
title: Test Collection 2
---
Dies ist die Beschreibung der zweiten Sammlung.`),
		},
	}

	collections := []*CollFacetType{
		{
			Id:    1,
			Title: "Test Collection 1",
		},
		{
			Id:    2,
			Title: "Test Collection 2",
		},
	}

	markdowns := map[string]map[string]string{
		"collection.test collection 1": {
			"type":            "collection",
			"collectiontitle": "Test Collection 1",
			"title":           "Test Collection 1",
			"path":            "pages/coll1.md",
		},
		"collection.test collection 2": {
			"type":            "collection",
			"collectiontitle": "Test Collection 2",
			"title":           "Test Collection 2",
			"path":            "pages/coll2.md",
		},
	}

	ctrl := &Controller{
		name:        "test",
		collections: collections,
		markdowns:   markdowns,
		pagesFS:     pagesFS,
	}

	ctrl.initMCP(router)

	ts := httptest.NewServer(router)
	defer ts.Close()

	clientTransport := &mcp.SSEClientTransport{
		Endpoint: ts.URL + "/mcp",
	}
	client := mcp.NewClient(&mcp.Implementation{
		Name:    "test client",
		Version: "0.0.1",
	}, nil)

	session, err := client.Connect(t.Context(), clientTransport, nil)
	if err != nil {
		t.Fatalf("failed to connect via SSE: %v", err)
	}
	defer session.Close()

	// Test 1: Query by collection_title
	res1, err := session.CallTool(t.Context(), &mcp.CallToolParams{
		Name: "get_collection_description",
		Arguments: map[string]any{
			"collection_title": "Test Collection 1",
		},
	})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}
	if res1.IsError {
		t.Fatalf("CallTool returned error")
	}
	var desc1 string
	raw1, _ := json.Marshal(res1.StructuredContent)
	if err := json.Unmarshal(raw1, &desc1); err != nil {
		t.Fatalf("failed to unmarshal result: %v", err)
	}
	if desc1 != "Dies ist die Beschreibung der ersten Sammlung." {
		t.Errorf("expected description 'Dies ist die Beschreibung der ersten Sammlung.', got '%s'", desc1)
	}

	// Test 2: Query by collection_id
	res2, err := session.CallTool(t.Context(), &mcp.CallToolParams{
		Name: "get_collection_description",
		Arguments: map[string]any{
			"collection_id": 2,
		},
	})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}
	if res2.IsError {
		t.Fatalf("CallTool returned error")
	}
	var desc2 string
	raw2, _ := json.Marshal(res2.StructuredContent)
	if err := json.Unmarshal(raw2, &desc2); err != nil {
		t.Fatalf("failed to unmarshal result: %v", err)
	}
	if desc2 != "Dies ist die Beschreibung der zweiten Sammlung." {
		t.Errorf("expected description 'Dies ist die Beschreibung der zweiten Sammlung.', got '%s'", desc2)
	}

	// Test 3: Query by alternative field "title"
	res3, err := session.CallTool(t.Context(), &mcp.CallToolParams{
		Name: "get_collection_description",
		Arguments: map[string]any{
			"title": "Test Collection 1",
		},
	})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}
	if res3.IsError {
		t.Fatalf("CallTool returned error")
	}
	var desc3 string
	raw3, _ := json.Marshal(res3.StructuredContent)
	if err := json.Unmarshal(raw3, &desc3); err != nil {
		t.Fatalf("failed to unmarshal result: %v", err)
	}
	if desc3 != "Dies ist die Beschreibung der ersten Sammlung." {
		t.Errorf("expected description 'Dies ist die Beschreibung der ersten Sammlung.', got '%s'", desc3)
	}

	// Test 4: Query by alternative field "id"
	res4, err := session.CallTool(t.Context(), &mcp.CallToolParams{
		Name: "get_collection_description",
		Arguments: map[string]any{
			"id": 2,
		},
	})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}
	if res4.IsError {
		t.Fatalf("CallTool returned error")
	}
	var desc4 string
	raw4, _ := json.Marshal(res4.StructuredContent)
	if err := json.Unmarshal(raw4, &desc4); err != nil {
		t.Fatalf("failed to unmarshal result: %v", err)
	}
	if desc4 != "Dies ist die Beschreibung der zweiten Sammlung." {
		t.Errorf("expected description 'Dies ist die Beschreibung der zweiten Sammlung.', got '%s'", desc4)
	}

	// Test 5: Query with not found
	res5, err := session.CallTool(t.Context(), &mcp.CallToolParams{
		Name: "get_collection_description",
		Arguments: map[string]any{
			"collection_id": 999,
		},
	})
	if err != nil {
		t.Fatalf("CallTool returned unexpected transport error: %v", err)
	}
	if !res5.IsError {
		t.Fatalf("expected error result for nonexistent collection")
	}

	// Test 6: Query with empty arguments
	res6, err := session.CallTool(t.Context(), &mcp.CallToolParams{
		Name:      "get_collection_description",
		Arguments: map[string]any{},
	})
	if err != nil {
		t.Fatalf("CallTool returned unexpected transport error: %v", err)
	}
	if !res6.IsError {
		t.Fatalf("expected error result for empty arguments")
	}
}
