package server

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/gin-gonic/gin"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func toMap(t *testing.T, val any) map[string]any {
	t.Helper()
	if val == nil {
		return nil
	}
	if m, ok := val.(map[string]any); ok {
		return m
	}
	raw, err := json.Marshal(val)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("failed to unmarshal to map: %v", err)
	}
	return m
}

func assertNoTypeArrays(t *testing.T, schema any, path string) {
	t.Helper()
	if schema == nil {
		return
	}
	switch v := schema.(type) {
	case map[string]any:
		if typeVal, ok := v["type"]; ok {
			if _, isArr := typeVal.([]any); isArr {
				t.Errorf("schema at %q contains array type: %v", path, typeVal)
			}
		}
		for k, child := range v {
			assertNoTypeArrays(t, child, path+"."+k)
		}
	case []any:
		for i, child := range v {
			assertNoTypeArrays(t, child, fmt.Sprintf("%s[%d]", path, i))
		}
	}
}

func assertAnyOfTypes(t *testing.T, obj map[string]any, path string, expectedTypes ...string) {
	t.Helper()
	anyOfVal, ok := obj["anyOf"]
	if !ok {
		t.Fatalf("expected 'anyOf' at %q, got: %+v", path, obj)
	}
	anyOfList, ok := anyOfVal.([]any)
	if !ok {
		t.Fatalf("expected 'anyOf' to be slice at %q, got: %T", path, anyOfVal)
	}
	if len(anyOfList) != len(expectedTypes) {
		t.Fatalf("expected %d anyOf branches at %q, got %d: %+v", len(expectedTypes), path, len(anyOfList), anyOfList)
	}
	foundTypes := make(map[string]bool)
	for _, item := range anyOfList {
		itemMap, ok := item.(map[string]any)
		if !ok {
			t.Fatalf("expected anyOf item to be map at %q, got: %T", path, item)
		}
		typ, ok := itemMap["type"].(string)
		if !ok {
			t.Fatalf("expected item type to be string at %q, got: %v", path, itemMap["type"])
		}
		foundTypes[typ] = true
	}
	for _, exp := range expectedTypes {
		if !foundTypes[exp] {
			t.Errorf("expected type %q in anyOf at %q, but found: %+v", exp, path, foundTypes)
		}
	}
}

func assertOutputSchemaObject(t *testing.T, tool *mcp.Tool) {
	t.Helper()
	if tool.OutputSchema == nil {
		t.Fatalf("tool %q has nil OutputSchema", tool.Name)
	}
	schemaMap, ok := tool.OutputSchema.(map[string]any)
	if !ok {
		raw, err := json.Marshal(tool.OutputSchema)
		if err != nil {
			t.Fatalf("tool %q failed to marshal OutputSchema: %v", tool.Name, err)
		}
		if err := json.Unmarshal(raw, &schemaMap); err != nil {
			t.Fatalf("tool %q failed to unmarshal OutputSchema to map: %v", tool.Name, err)
		}
	}
	if schemaMap["type"] != "object" {
		t.Errorf("tool %q expected OutputSchema type 'object', got %v", tool.Name, schemaMap["type"])
	}
}

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

	clientTransport := &mcp.StreamableClientTransport{
		Endpoint: ts.URL + "/mcp",
	}
	client := mcp.NewClient(&mcp.Implementation{
		Name:    "test client",
		Version: "0.0.1",
	}, nil)

	session, err := client.Connect(t.Context(), clientTransport, nil)
	if err != nil {
		t.Fatalf("failed to connect via StreamableHTTP: %v", err)
	}
	defer session.Close()

	toolsList, err := session.ListTools(t.Context(), &mcp.ListToolsParams{})
	if err != nil {
		t.Fatalf("failed to list tools: %v", err)
	}

	var foundTool *mcp.Tool
	for _, tool := range toolsList.Tools {
		if tool.Name == "get_collections" {
			foundTool = tool
			break
		}
	}
	if foundTool == nil {
		t.Fatalf("tool 'get_collections' not found in tools list")
	}
	assertOutputSchemaObject(t, foundTool)

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
	var resultColls GetCollectionsResult
	if err := json.Unmarshal(raw, &resultColls); err != nil {
		t.Fatalf("failed to unmarshal structured content: %v", err)
	}

	if len(resultColls.Collections) != 2 {
		t.Fatalf("expected 2 collections, got %d", len(resultColls.Collections))
	}
	if resultColls.Collections[0].Id != 1 || resultColls.Collections[0].Title != "Test Collection 1" {
		t.Errorf("expected collection[0] id=1 title='Test Collection 1', got %+v", resultColls.Collections[0])
	}
	if resultColls.Collections[1].Id != 2 || resultColls.Collections[1].Title != "Test Collection 2" {
		t.Errorf("expected collection[1] id=2 title='Test Collection 2', got %+v", resultColls.Collections[1])
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

	clientTransport := &mcp.StreamableClientTransport{
		Endpoint: ts.URL + "/mcp",
	}
	client := mcp.NewClient(&mcp.Implementation{
		Name:    "test client",
		Version: "0.0.1",
	}, nil)

	session, err := client.Connect(t.Context(), clientTransport, nil)
	if err != nil {
		t.Fatalf("failed to connect via StreamableHTTP: %v", err)
	}
	defer session.Close()

	toolsList, err := session.ListTools(t.Context(), &mcp.ListToolsParams{})
	if err != nil {
		t.Fatalf("failed to list tools: %v", err)
	}
	var foundTool *mcp.Tool
	for _, tool := range toolsList.Tools {
		if tool.Name == "get_collection_description" {
			foundTool = tool
			break
		}
	}
	if foundTool == nil {
		t.Fatalf("tool 'get_collection_description' not found in tools list")
	}
	assertOutputSchemaObject(t, foundTool)
	if !strings.Contains(foundTool.Description, "Markdown") {
		t.Errorf("expected tool description to mention Markdown, got %q", foundTool.Description)
	}

	// Test 1: Query by title
	res1, err := session.CallTool(t.Context(), &mcp.CallToolParams{
		Name: "get_collection_description",
		Arguments: map[string]any{
			"title": "Test Collection 1",
		},
	})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}
	if res1.IsError {
		t.Fatalf("CallTool returned error")
	}
	var desc1 GetCollectionDescriptionResult
	raw1, _ := json.Marshal(res1.StructuredContent)
	if err := json.Unmarshal(raw1, &desc1); err != nil {
		t.Fatalf("failed to unmarshal result: %v", err)
	}
	if desc1.Description != "Dies ist die Beschreibung der ersten Sammlung." {
		t.Errorf("expected description 'Dies ist die Beschreibung der ersten Sammlung.', got '%s'", desc1.Description)
	}
	if len(res1.Content) == 0 {
		t.Fatalf("expected at least 1 content item in res1")
	}
	embedded1, ok := res1.Content[0].(*mcp.EmbeddedResource)
	if !ok {
		t.Fatalf("expected Content[0] to be *mcp.EmbeddedResource, got %T", res1.Content[0])
	}
	if embedded1.Resource == nil {
		t.Fatalf("expected embedded1.Resource not to be nil")
	}
	if embedded1.Resource.MIMEType != "text/markdown" {
		t.Errorf("expected MIMEType 'text/markdown', got %q", embedded1.Resource.MIMEType)
	}
	if embedded1.Resource.URI != "collection://Test Collection 1" {
		t.Errorf("expected URI 'collection://Test Collection 1', got %q", embedded1.Resource.URI)
	}
	if embedded1.Resource.Text != "Dies ist die Beschreibung der ersten Sammlung." {
		t.Errorf("expected Resource.Text 'Dies ist die Beschreibung der ersten Sammlung.', got %q", embedded1.Resource.Text)
	}

	// Test 2: Query by id
	res2, err := session.CallTool(t.Context(), &mcp.CallToolParams{
		Name: "get_collection_description",
		Arguments: map[string]any{
			"id": 2,
		},
	})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}
	if res2.IsError {
		t.Fatalf("CallTool returned error")
	}
	var desc2 GetCollectionDescriptionResult
	raw2, _ := json.Marshal(res2.StructuredContent)
	if err := json.Unmarshal(raw2, &desc2); err != nil {
		t.Fatalf("failed to unmarshal result: %v", err)
	}
	if desc2.Description != "Dies ist die Beschreibung der zweiten Sammlung." {
		t.Errorf("expected description 'Dies ist die Beschreibung der zweiten Sammlung.', got '%s'", desc2.Description)
	}
	if len(res2.Content) == 0 {
		t.Fatalf("expected at least 1 content item in res2")
	}
	embedded2, ok := res2.Content[0].(*mcp.EmbeddedResource)
	if !ok {
		t.Fatalf("expected Content[0] to be *mcp.EmbeddedResource, got %T", res2.Content[0])
	}
	if embedded2.Resource == nil {
		t.Fatalf("expected embedded2.Resource not to be nil")
	}
	if embedded2.Resource.MIMEType != "text/markdown" {
		t.Errorf("expected MIMEType 'text/markdown', got %q", embedded2.Resource.MIMEType)
	}
	if embedded2.Resource.URI != "collection://2" {
		t.Errorf("expected URI 'collection://2', got %q", embedded2.Resource.URI)
	}
	if embedded2.Resource.Text != "Dies ist die Beschreibung der zweiten Sammlung." {
		t.Errorf("expected Resource.Text 'Dies ist die Beschreibung der zweiten Sammlung.', got %q", embedded2.Resource.Text)
	}

	// Test 3: Query with not found
	res3, err := session.CallTool(t.Context(), &mcp.CallToolParams{
		Name: "get_collection_description",
		Arguments: map[string]any{
			"id": 999,
		},
	})
	if err != nil {
		t.Fatalf("CallTool returned unexpected transport error: %v", err)
	}
	if !res3.IsError {
		t.Fatalf("expected error result for nonexistent collection")
	}

	// Test 4: Query with empty arguments
	res4, err := session.CallTool(t.Context(), &mcp.CallToolParams{
		Name:      "get_collection_description",
		Arguments: map[string]any{},
	})
	if err != nil {
		t.Fatalf("CallTool returned unexpected transport error: %v", err)
	}
	if !res4.IsError {
		t.Fatalf("expected error result for empty arguments")
	}
}

func TestGetEstatesTool(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()

	estates := []*CollFacetType{
		{
			Id:         10,
			Title:      "Test Estate 1",
			Identifier: "cat:\"estate1\"",
			Url:        "https://example.com/estate1",
		},
		{
			Id:         20,
			Title:      "Test Estate 2",
			Identifier: "cat:\"estate2\"",
			Url:        "https://example.com/estate2",
		},
	}

	ctrl := &Controller{
		name:    "test",
		estates: estates,
	}

	ctrl.initMCP(router)

	ts := httptest.NewServer(router)
	defer ts.Close()

	clientTransport := &mcp.StreamableClientTransport{
		Endpoint: ts.URL + "/mcp",
	}
	client := mcp.NewClient(&mcp.Implementation{
		Name:    "test client",
		Version: "0.0.1",
	}, nil)

	session, err := client.Connect(t.Context(), clientTransport, nil)
	if err != nil {
		t.Fatalf("failed to connect via StreamableHTTP: %v", err)
	}
	defer session.Close()

	toolsList, err := session.ListTools(t.Context(), &mcp.ListToolsParams{})
	if err != nil {
		t.Fatalf("failed to list tools: %v", err)
	}

	var foundTool *mcp.Tool
	for _, tool := range toolsList.Tools {
		if tool.Name == "get_estates" {
			foundTool = tool
			break
		}
	}
	if foundTool == nil {
		t.Fatalf("tool 'get_estates' not found in tools list")
	}
	assertOutputSchemaObject(t, foundTool)

	res, err := session.CallTool(t.Context(), &mcp.CallToolParams{
		Name: "get_estates",
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
	var resultEstates GetEstatesResult
	if err := json.Unmarshal(raw, &resultEstates); err != nil {
		t.Fatalf("failed to unmarshal structured content: %v", err)
	}

	if len(resultEstates.Estates) != 2 {
		t.Fatalf("expected 2 estates, got %d", len(resultEstates.Estates))
	}
	if resultEstates.Estates[0].Id != 10 || resultEstates.Estates[0].Title != "Test Estate 1" {
		t.Errorf("expected estate[0] id=10 title='Test Estate 1', got %+v", resultEstates.Estates[0])
	}
	if resultEstates.Estates[1].Id != 20 || resultEstates.Estates[1].Title != "Test Estate 2" {
		t.Errorf("expected estate[1] id=20 title='Test Estate 2', got %+v", resultEstates.Estates[1])
	}
}

func TestGetEstateDescriptionTool(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()

	pagesFS := fstest.MapFS{
		"pages/estate1.md": &fstest.MapFile{
			Data: []byte(`---
type: estate
estatetitle: Test Estate 1
title: Test Estate 1
estateid: 10
---
Dies ist die Beschreibung des ersten Nachlasses.`),
		},
		"pages/estate2.md": &fstest.MapFile{
			Data: []byte(`---
type: estate
estatetitle: Test Estate 2
title: Test Estate 2
estateid: 20
---
Dies ist die Beschreibung des zweiten Nachlasses.`),
		},
		"pages/estate3.md": &fstest.MapFile{
			Data: []byte(`---
type: estate
estatetitle: Fallback Estate
description: Dies ist die Fallback-Beschreibung aus Meta.
---
`),
		},
	}

	estates := []*CollFacetType{
		{
			Id:    10,
			Title: "Test Estate 1",
		},
		{
			Id:    20,
			Title: "Test Estate 2",
		},
		{
			Id:    30,
			Title: "Fallback Estate",
		},
	}

	markdowns := map[string]map[string]string{
		"estate.test estate 1": {
			"type":        "estate",
			"estatetitle": "Test Estate 1",
			"title":       "Test Estate 1",
			"estateid":    "10",
			"path":        "pages/estate1.md",
		},
		"estate.test estate 2": {
			"type":        "estate",
			"estatetitle": "Test Estate 2",
			"title":       "Test Estate 2",
			"estateid":    "20",
			"path":        "pages/estate2.md",
		},
		"estate.fallback estate": {
			"type":        "estate",
			"estatetitle": "Fallback Estate",
			"title":       "Fallback Estate",
			"description": "Dies ist die Fallback-Beschreibung aus Meta.",
			"path":        "pages/estate3.md",
		},
	}

	ctrl := &Controller{
		name:      "test",
		estates:   estates,
		markdowns: markdowns,
		pagesFS:   pagesFS,
	}

	ctrl.initMCP(router)

	ts := httptest.NewServer(router)
	defer ts.Close()

	clientTransport := &mcp.StreamableClientTransport{
		Endpoint: ts.URL + "/mcp",
	}
	client := mcp.NewClient(&mcp.Implementation{
		Name:    "test client",
		Version: "0.0.1",
	}, nil)

	session, err := client.Connect(t.Context(), clientTransport, nil)
	if err != nil {
		t.Fatalf("failed to connect via StreamableHTTP: %v", err)
	}
	defer session.Close()

	toolsList, err := session.ListTools(t.Context(), &mcp.ListToolsParams{})
	if err != nil {
		t.Fatalf("failed to list tools: %v", err)
	}
	var foundTool *mcp.Tool
	for _, tool := range toolsList.Tools {
		if tool.Name == "get_estate_description" {
			foundTool = tool
			break
		}
	}
	if foundTool == nil {
		t.Fatalf("tool 'get_estate_description' not found in tools list")
	}
	assertOutputSchemaObject(t, foundTool)
	if !strings.Contains(foundTool.Description, "Markdown") {
		t.Errorf("expected tool description to mention Markdown, got %q", foundTool.Description)
	}

	// Test 1: Query by title
	res1, err := session.CallTool(t.Context(), &mcp.CallToolParams{
		Name: "get_estate_description",
		Arguments: map[string]any{
			"title": "Test Estate 1",
		},
	})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}
	if res1.IsError {
		t.Fatalf("CallTool returned error")
	}
	var desc1 GetEstateDescriptionResult
	raw1, _ := json.Marshal(res1.StructuredContent)
	if err := json.Unmarshal(raw1, &desc1); err != nil {
		t.Fatalf("failed to unmarshal result: %v", err)
	}
	if desc1.Description != "Dies ist die Beschreibung des ersten Nachlasses." {
		t.Errorf("expected description 'Dies ist die Beschreibung des ersten Nachlasses.', got '%s'", desc1.Description)
	}
	if len(res1.Content) == 0 {
		t.Fatalf("expected at least 1 content item in res1")
	}
	embedded1, ok := res1.Content[0].(*mcp.EmbeddedResource)
	if !ok {
		t.Fatalf("expected Content[0] to be *mcp.EmbeddedResource, got %T", res1.Content[0])
	}
	if embedded1.Resource == nil {
		t.Fatalf("expected embedded1.Resource not to be nil")
	}
	if embedded1.Resource.MIMEType != "text/markdown" {
		t.Errorf("expected MIMEType 'text/markdown', got %q", embedded1.Resource.MIMEType)
	}
	if embedded1.Resource.URI != "estate://Test Estate 1" {
		t.Errorf("expected URI 'estate://Test Estate 1', got %q", embedded1.Resource.URI)
	}
	if embedded1.Resource.Text != "Dies ist die Beschreibung des ersten Nachlasses." {
		t.Errorf("expected Resource.Text 'Dies ist die Beschreibung des ersten Nachlasses.', got %q", embedded1.Resource.Text)
	}

	// Test 2: Query by id
	res2, err := session.CallTool(t.Context(), &mcp.CallToolParams{
		Name: "get_estate_description",
		Arguments: map[string]any{
			"id": 20,
		},
	})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}
	if res2.IsError {
		t.Fatalf("CallTool returned error")
	}
	var desc2 GetEstateDescriptionResult
	raw2, _ := json.Marshal(res2.StructuredContent)
	if err := json.Unmarshal(raw2, &desc2); err != nil {
		t.Fatalf("failed to unmarshal result: %v", err)
	}
	if desc2.Description != "Dies ist die Beschreibung des zweiten Nachlasses." {
		t.Errorf("expected description 'Dies ist die Beschreibung des zweiten Nachlasses.', got '%s'", desc2.Description)
	}
	if len(res2.Content) == 0 {
		t.Fatalf("expected at least 1 content item in res2")
	}
	embedded2, ok := res2.Content[0].(*mcp.EmbeddedResource)
	if !ok {
		t.Fatalf("expected Content[0] to be *mcp.EmbeddedResource, got %T", res2.Content[0])
	}
	if embedded2.Resource == nil {
		t.Fatalf("expected embedded2.Resource not to be nil")
	}
	if embedded2.Resource.MIMEType != "text/markdown" {
		t.Errorf("expected MIMEType 'text/markdown', got %q", embedded2.Resource.MIMEType)
	}
	if embedded2.Resource.URI != "estate://20" {
		t.Errorf("expected URI 'estate://20', got %q", embedded2.Resource.URI)
	}
	if embedded2.Resource.Text != "Dies ist die Beschreibung des zweiten Nachlasses." {
		t.Errorf("expected Resource.Text 'Dies ist die Beschreibung des zweiten Nachlasses.', got %q", embedded2.Resource.Text)
	}

	// Test 3: Query numeric string in title
	res3, err := session.CallTool(t.Context(), &mcp.CallToolParams{
		Name: "get_estate_description",
		Arguments: map[string]any{
			"title": "20",
		},
	})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}
	if res3.IsError {
		t.Fatalf("CallTool returned error: %v", res3)
	}
	var desc3 GetEstateDescriptionResult
	raw3, _ := json.Marshal(res3.StructuredContent)
	if err := json.Unmarshal(raw3, &desc3); err != nil {
		t.Fatalf("failed to unmarshal result: %v", err)
	}
	if desc3.Description != "Dies ist die Beschreibung des zweiten Nachlasses." {
		t.Errorf("expected description 'Dies ist die Beschreibung des zweiten Nachlasses.', got '%s'", desc3.Description)
	}

	// Test 4: Fallback to metadata description
	res4, err := session.CallTool(t.Context(), &mcp.CallToolParams{
		Name: "get_estate_description",
		Arguments: map[string]any{
			"title": "Fallback Estate",
		},
	})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}
	if res4.IsError {
		t.Fatalf("CallTool returned error")
	}
	var desc4 GetEstateDescriptionResult
	raw4, _ := json.Marshal(res4.StructuredContent)
	if err := json.Unmarshal(raw4, &desc4); err != nil {
		t.Fatalf("failed to unmarshal result: %v", err)
	}
	if desc4.Description != "Dies ist die Fallback-Beschreibung aus Meta." {
		t.Errorf("expected description 'Dies ist die Fallback-Beschreibung aus Meta.', got '%s'", desc4.Description)
	}

	// Test 5: Query with not found
	res5, err := session.CallTool(t.Context(), &mcp.CallToolParams{
		Name: "get_estate_description",
		Arguments: map[string]any{
			"id": 999,
		},
	})
	if err != nil {
		t.Fatalf("CallTool returned unexpected transport error: %v", err)
	}
	if !res5.IsError {
		t.Fatalf("expected error result for nonexistent estate")
	}

	// Test 6: Query with empty arguments
	res6, err := session.CallTool(t.Context(), &mcp.CallToolParams{
		Name:      "get_estate_description",
		Arguments: map[string]any{},
	})
	if err != nil {
		t.Fatalf("CallTool returned unexpected transport error: %v", err)
	}
	if !res6.IsError {
		t.Fatalf("expected error result for empty arguments")
	}
}

func TestGetTopicsTool(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()

	topics := []*CollFacetType{
		{
			Id:         10,
			Title:      "Test Topic 1",
			Identifier: "voc:\"topic1\"",
			Url:        "https://example.com/topic1",
		},
		{
			Id:         20,
			Title:      "Test Topic 2",
			Identifier: "voc:\"topic2\"",
			Url:        "https://example.com/topic2",
		},
	}

	ctrl := &Controller{
		name:   "test",
		topics: topics,
	}

	ctrl.initMCP(router)

	ts := httptest.NewServer(router)
	defer ts.Close()

	clientTransport := &mcp.StreamableClientTransport{
		Endpoint: ts.URL + "/mcp",
	}
	client := mcp.NewClient(&mcp.Implementation{
		Name:    "test client",
		Version: "0.0.1",
	}, nil)

	session, err := client.Connect(t.Context(), clientTransport, nil)
	if err != nil {
		t.Fatalf("failed to connect via StreamableHTTP: %v", err)
	}
	defer session.Close()

	toolsList, err := session.ListTools(t.Context(), &mcp.ListToolsParams{})
	if err != nil {
		t.Fatalf("failed to list tools: %v", err)
	}

	var foundTool *mcp.Tool
	for _, tool := range toolsList.Tools {
		if tool.Name == "get_topics" {
			foundTool = tool
			break
		}
	}
	if foundTool == nil {
		t.Fatalf("tool 'get_topics' not found in tools list")
	}
	assertOutputSchemaObject(t, foundTool)

	res, err := session.CallTool(t.Context(), &mcp.CallToolParams{
		Name: "get_topics",
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
	var resultTopics GetTopicsResult
	if err := json.Unmarshal(raw, &resultTopics); err != nil {
		t.Fatalf("failed to unmarshal structured content: %v", err)
	}

	if len(resultTopics.Topics) != 2 {
		t.Fatalf("expected 2 topics, got %d", len(resultTopics.Topics))
	}
	if resultTopics.Topics[0].Id != 10 || resultTopics.Topics[0].Title != "Test Topic 1" {
		t.Errorf("expected topic[0] id=10 title='Test Topic 1', got %+v", resultTopics.Topics[0])
	}
	if resultTopics.Topics[1].Id != 20 || resultTopics.Topics[1].Title != "Test Topic 2" {
		t.Errorf("expected topic[1] id=20 title='Test Topic 2', got %+v", resultTopics.Topics[1])
	}
}

func TestGetTopicDescriptionTool(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()

	pagesFS := fstest.MapFS{
		"pages/topic1.md": &fstest.MapFile{
			Data: []byte(`---
type: topic
topictitle: Test Topic 1
title: Test Topic 1
topicid: 10
---
Dies ist die Beschreibung des ersten Themas.`),
		},
		"pages/topic2.md": &fstest.MapFile{
			Data: []byte(`---
type: topic
topictitle: Test Topic 2
title: Test Topic 2
topicid: 20
---
Dies ist die Beschreibung des zweiten Themas.`),
		},
		"pages/topic3.md": &fstest.MapFile{
			Data: []byte(`---
type: topic
topictitle: Fallback Topic
title: Fallback Topic
description: Dies ist die Fallback-Beschreibung aus Meta.
---`),
		},
	}

	topics := []*CollFacetType{
		{
			Id:    10,
			Title: "Test Topic 1",
		},
		{
			Id:    20,
			Title: "Test Topic 2",
		},
		{
			Id:    30,
			Title: "Fallback Topic",
		},
	}

	markdowns := map[string]map[string]string{
		"topic.test topic 1": {
			"type":       "topic",
			"topictitle": "Test Topic 1",
			"title":      "Test Topic 1",
			"topicid":    "10",
			"path":       "pages/topic1.md",
		},
		"topic.test topic 2": {
			"type":       "topic",
			"topictitle": "Test Topic 2",
			"title":      "Test Topic 2",
			"topicid":    "20",
			"path":       "pages/topic2.md",
		},
		"topic.fallback topic": {
			"type":        "topic",
			"topictitle":  "Fallback Topic",
			"title":       "Fallback Topic",
			"description": "Dies ist die Fallback-Beschreibung aus Meta.",
			"path":        "pages/topic3.md",
		},
	}

	ctrl := &Controller{
		name:      "test",
		topics:    topics,
		markdowns: markdowns,
		pagesFS:   pagesFS,
	}

	ctrl.initMCP(router)

	ts := httptest.NewServer(router)
	defer ts.Close()

	clientTransport := &mcp.StreamableClientTransport{
		Endpoint: ts.URL + "/mcp",
	}
	client := mcp.NewClient(&mcp.Implementation{
		Name:    "test client",
		Version: "0.0.1",
	}, nil)

	session, err := client.Connect(t.Context(), clientTransport, nil)
	if err != nil {
		t.Fatalf("failed to connect via StreamableHTTP: %v", err)
	}
	defer session.Close()

	toolsList, err := session.ListTools(t.Context(), &mcp.ListToolsParams{})
	if err != nil {
		t.Fatalf("failed to list tools: %v", err)
	}
	var foundTool *mcp.Tool
	for _, tool := range toolsList.Tools {
		if tool.Name == "get_topic_description" {
			foundTool = tool
			break
		}
	}
	if foundTool == nil {
		t.Fatalf("tool 'get_topic_description' not found in tools list")
	}
	assertOutputSchemaObject(t, foundTool)
	if !strings.Contains(foundTool.Description, "Markdown") {
		t.Errorf("expected tool description to mention Markdown, got %q", foundTool.Description)
	}

	// Test 1: Query by title
	res1, err := session.CallTool(t.Context(), &mcp.CallToolParams{
		Name: "get_topic_description",
		Arguments: map[string]any{
			"title": "Test Topic 1",
		},
	})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}
	if res1.IsError {
		t.Fatalf("CallTool returned error")
	}
	var desc1 GetTopicDescriptionResult
	raw1, _ := json.Marshal(res1.StructuredContent)
	if err := json.Unmarshal(raw1, &desc1); err != nil {
		t.Fatalf("failed to unmarshal result: %v", err)
	}
	if desc1.Description != "Dies ist die Beschreibung des ersten Themas." {
		t.Errorf("expected description 'Dies ist die Beschreibung des ersten Themas.', got '%s'", desc1.Description)
	}
	if len(res1.Content) == 0 {
		t.Fatalf("expected at least 1 content item in res1")
	}
	embedded1, ok := res1.Content[0].(*mcp.EmbeddedResource)
	if !ok {
		t.Fatalf("expected Content[0] to be *mcp.EmbeddedResource, got %T", res1.Content[0])
	}
	if embedded1.Resource == nil {
		t.Fatalf("expected embedded1.Resource not to be nil")
	}
	if embedded1.Resource.MIMEType != "text/markdown" {
		t.Errorf("expected MIMEType 'text/markdown', got %q", embedded1.Resource.MIMEType)
	}
	if embedded1.Resource.URI != "topic://Test Topic 1" {
		t.Errorf("expected URI 'topic://Test Topic 1', got %q", embedded1.Resource.URI)
	}
	if embedded1.Resource.Text != "Dies ist die Beschreibung des ersten Themas." {
		t.Errorf("expected Resource.Text 'Dies ist die Beschreibung des ersten Themas.', got %q", embedded1.Resource.Text)
	}

	// Test 2: Query by id
	res2, err := session.CallTool(t.Context(), &mcp.CallToolParams{
		Name: "get_topic_description",
		Arguments: map[string]any{
			"id": 20,
		},
	})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}
	if res2.IsError {
		t.Fatalf("CallTool returned error")
	}
	var desc2 GetTopicDescriptionResult
	raw2, _ := json.Marshal(res2.StructuredContent)
	if err := json.Unmarshal(raw2, &desc2); err != nil {
		t.Fatalf("failed to unmarshal result: %v", err)
	}
	if desc2.Description != "Dies ist die Beschreibung des zweiten Themas." {
		t.Errorf("expected description 'Dies ist die Beschreibung des zweiten Themas.', got '%s'", desc2.Description)
	}
	if len(res2.Content) == 0 {
		t.Fatalf("expected at least 1 content item in res2")
	}
	embedded2, ok := res2.Content[0].(*mcp.EmbeddedResource)
	if !ok {
		t.Fatalf("expected Content[0] to be *mcp.EmbeddedResource, got %T", res2.Content[0])
	}
	if embedded2.Resource == nil {
		t.Fatalf("expected embedded2.Resource not to be nil")
	}
	if embedded2.Resource.MIMEType != "text/markdown" {
		t.Errorf("expected MIMEType 'text/markdown', got %q", embedded2.Resource.MIMEType)
	}
	if embedded2.Resource.URI != "topic://20" {
		t.Errorf("expected URI 'topic://20', got %q", embedded2.Resource.URI)
	}
	if embedded2.Resource.Text != "Dies ist die Beschreibung des zweiten Themas." {
		t.Errorf("expected Resource.Text 'Dies ist die Beschreibung des zweiten Themas.', got %q", embedded2.Resource.Text)
	}

	// Test 3: Query numeric string in title
	res3, err := session.CallTool(t.Context(), &mcp.CallToolParams{
		Name: "get_topic_description",
		Arguments: map[string]any{
			"title": "20",
		},
	})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}
	if res3.IsError {
		t.Fatalf("CallTool returned error: %v", res3)
	}
	var desc3 GetTopicDescriptionResult
	raw3, _ := json.Marshal(res3.StructuredContent)
	if err := json.Unmarshal(raw3, &desc3); err != nil {
		t.Fatalf("failed to unmarshal result: %v", err)
	}
	if desc3.Description != "Dies ist die Beschreibung des zweiten Themas." {
		t.Errorf("expected description 'Dies ist die Beschreibung des zweiten Themas.', got '%s'", desc3.Description)
	}

	// Test 4: Fallback to metadata description
	res4, err := session.CallTool(t.Context(), &mcp.CallToolParams{
		Name: "get_topic_description",
		Arguments: map[string]any{
			"title": "Fallback Topic",
		},
	})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}
	if res4.IsError {
		t.Fatalf("CallTool returned error")
	}
	var desc4 GetTopicDescriptionResult
	raw4, _ := json.Marshal(res4.StructuredContent)
	if err := json.Unmarshal(raw4, &desc4); err != nil {
		t.Fatalf("failed to unmarshal result: %v", err)
	}
	if desc4.Description != "Dies ist die Fallback-Beschreibung aus Meta." {
		t.Errorf("expected description 'Dies ist die Fallback-Beschreibung aus Meta.', got '%s'", desc4.Description)
	}

	// Test 5: Query with not found
	res5, err := session.CallTool(t.Context(), &mcp.CallToolParams{
		Name: "get_topic_description",
		Arguments: map[string]any{
			"id": 999,
		},
	})
	if err != nil {
		t.Fatalf("CallTool returned unexpected transport error: %v", err)
	}
	if !res5.IsError {
		t.Fatalf("expected error result for nonexistent topic")
	}

	// Test 6: Query with empty arguments
	res6, err := session.CallTool(t.Context(), &mcp.CallToolParams{
		Name:      "get_topic_description",
		Arguments: map[string]any{},
	})
	if err != nil {
		t.Fatalf("CallTool returned unexpected transport error: %v", err)
	}
	if !res6.IsError {
		t.Fatalf("expected error result for empty arguments")
	}
}

func TestControllerInitMarkdownIndexing(t *testing.T) {
	pagesFS := fstest.MapFS{
		"pages/estate1.md": &fstest.MapFile{
			Data: []byte(`---
type: estate
estatetitle: My Estate
---
Content of my estate`),
		},
		"pages/coll1.md": &fstest.MapFile{
			Data: []byte(`---
type: collection
collectiontitle: My Collection
---
Content of my collection`),
		},
		"pages/topic1.md": &fstest.MapFile{
			Data: []byte(`---
type: topic
topictitle: My Topic
---
Content of my topic`),
		},
		"pages/other.md": &fstest.MapFile{
			Data: []byte(`---
type: generic
title: Other Item
---
Content of other item`),
		},
	}

	ctrl := &Controller{
		name:       "test",
		pagesFS:    pagesFS,
		templateFS: fstest.MapFS{},
		staticFS:   fstest.MapFS{},
	}

	if err := ctrl.init(); err != nil {
		t.Fatalf("ctrl.init() failed: %v", err)
	}

	if len(ctrl.markdowns) != 4 {
		t.Fatalf("expected 4 indexed markdowns, got %d: %v", len(ctrl.markdowns), ctrl.markdowns)
	}

	if _, ok := ctrl.markdowns["estate.my estate"]; !ok {
		t.Errorf("expected key 'estate.my estate' in markdowns")
	}
	if _, ok := ctrl.markdowns["collection.my collection"]; !ok {
		t.Errorf("expected key 'collection.my collection' in markdowns")
	}
	if _, ok := ctrl.markdowns["topic.my topic"]; !ok {
		t.Errorf("expected key 'topic.my topic' in markdowns")
	}
	if _, ok := ctrl.markdowns["generic.other item"]; !ok {
		t.Errorf("expected key 'generic.other item' in markdowns")
	}
}

func TestGetEstatesTool_FromMarkdowns(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()

	pagesFS := fstest.MapFS{
		"pages/estates/100_samuel_herzog.md": &fstest.MapFile{
			Data: []byte(`---
type: estate
estatetitle: Samuel Herzog
estateid: 100
identifier: 'cat:"herzog"'
url: /pages/estates/100_samuel_herzog
image: /images/herzog.jpg
contact: info@example.com
count: 42
---
Beschreibung Samuel Herzog`),
		},
		"pages/estates/200_anna_meier.md": &fstest.MapFile{
			Data: []byte(`---
type: estate
title: Anna Meier
id: 200
identifier: 'cat:"meier"'
---
Beschreibung Anna Meier`),
		},
		"pages/collections/85_poster.md": &fstest.MapFile{
			Data: []byte(`---
type: collection
collectiontitle: Plakatsammlung
id: 85
---
Beschreibung Plakate`),
		},
		"pages/topics/30_art.md": &fstest.MapFile{
			Data: []byte(`---
type: topic
topictitle: Kunstgeschichte
topicid: 30
identifier: 'voc:"art"'
url: /pages/topics/30_art
count: 15
---
Beschreibung Kunstgeschichte`),
		},
	}

	ctrl := &Controller{
		name:       "test",
		pagesFS:    pagesFS,
		templateFS: fstest.MapFS{},
		staticFS:   fstest.MapFS{},
		estates:    nil, // intentionally nil to test dynamic markdown discovery
	}

	if err := ctrl.init(); err != nil {
		t.Fatalf("ctrl.init() failed: %v", err)
	}

	ctrl.initMCP(router)

	ts := httptest.NewServer(router)
	defer ts.Close()

	clientTransport := &mcp.StreamableClientTransport{
		Endpoint: ts.URL + "/mcp",
	}
	client := mcp.NewClient(&mcp.Implementation{
		Name:    "test client",
		Version: "0.0.1",
	}, nil)

	session, err := client.Connect(t.Context(), clientTransport, nil)
	if err != nil {
		t.Fatalf("failed to connect via StreamableHTTP: %v", err)
	}
	defer session.Close()

	// 1. Call get_estates and verify dynamic population from markdowns
	res, err := session.CallTool(t.Context(), &mcp.CallToolParams{
		Name: "get_estates",
	})
	if err != nil {
		t.Fatalf("CallTool get_estates failed: %v", err)
	}
	if res.IsError {
		t.Fatalf("CallTool get_estates returned error")
	}

	raw, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatalf("failed to marshal structured content: %v", err)
	}
	var resultEstates GetEstatesResult
	if err := json.Unmarshal(raw, &resultEstates); err != nil {
		t.Fatalf("failed to unmarshal structured content: %v", err)
	}

	if len(resultEstates.Estates) != 2 {
		t.Fatalf("expected 2 discovered estates, got %d", len(resultEstates.Estates))
	}
	if resultEstates.Estates[0].Id != 100 || resultEstates.Estates[0].Title != "Samuel Herzog" {
		t.Errorf("unexpected estate[0]: %+v", resultEstates.Estates[0])
	}
	if resultEstates.Estates[1].Id != 200 || resultEstates.Estates[1].Title != "Anna Meier" {
		t.Errorf("unexpected estate[1]: %+v", resultEstates.Estates[1])
	}

	// 2. Call get_estate_description by ID on discovered estate
	descRes, err := session.CallTool(t.Context(), &mcp.CallToolParams{
		Name: "get_estate_description",
		Arguments: map[string]any{
			"id": 100,
		},
	})
	if err != nil {
		t.Fatalf("CallTool get_estate_description failed: %v", err)
	}
	if descRes.IsError {
		t.Fatalf("CallTool get_estate_description returned error")
	}
	var desc GetEstateDescriptionResult
	rawDesc, _ := json.Marshal(descRes.StructuredContent)
	if err := json.Unmarshal(rawDesc, &desc); err != nil {
		t.Fatalf("failed to unmarshal description: %v", err)
	}
	if desc.Description != "Beschreibung Samuel Herzog" {
		t.Errorf("expected 'Beschreibung Samuel Herzog', got '%s'", desc.Description)
	}

	// 3. Call get_collections and verify dynamic population from markdowns
	collRes, err := session.CallTool(t.Context(), &mcp.CallToolParams{
		Name: "get_collections",
	})
	if err != nil {
		t.Fatalf("CallTool get_collections failed: %v", err)
	}
	if collRes.IsError {
		t.Fatalf("CallTool get_collections returned error")
	}
	var resultCollections GetCollectionsResult
	rawColl, _ := json.Marshal(collRes.StructuredContent)
	if err := json.Unmarshal(rawColl, &resultCollections); err != nil {
		t.Fatalf("failed to unmarshal structured collections: %v", err)
	}
	if len(resultCollections.Collections) != 1 || resultCollections.Collections[0].Id != 85 || resultCollections.Collections[0].Title != "Plakatsammlung" {
		t.Errorf("unexpected collections: %+v", resultCollections.Collections)
	}

	// 4. Call get_topics and verify dynamic population from markdowns
	topRes, err := session.CallTool(t.Context(), &mcp.CallToolParams{
		Name: "get_topics",
	})
	if err != nil {
		t.Fatalf("CallTool get_topics failed: %v", err)
	}
	if topRes.IsError {
		t.Fatalf("CallTool get_topics returned error")
	}
	var resultTopics GetTopicsResult
	rawTop, _ := json.Marshal(topRes.StructuredContent)
	if err := json.Unmarshal(rawTop, &resultTopics); err != nil {
		t.Fatalf("failed to unmarshal structured topics: %v", err)
	}
	if len(resultTopics.Topics) != 1 || resultTopics.Topics[0].Id != 30 || resultTopics.Topics[0].Title != "Kunstgeschichte" {
		t.Errorf("unexpected topics: %+v", resultTopics.Topics)
	}

	// 5. Call get_topic_description by ID on discovered topic
	topicDescRes, err := session.CallTool(t.Context(), &mcp.CallToolParams{
		Name: "get_topic_description",
		Arguments: map[string]any{
			"id": 30,
		},
	})
	if err != nil {
		t.Fatalf("CallTool get_topic_description failed: %v", err)
	}
	if topicDescRes.IsError {
		t.Fatalf("CallTool get_topic_description returned error")
	}
	var topicDesc GetTopicDescriptionResult
	rawTopicDesc, _ := json.Marshal(topicDescRes.StructuredContent)
	if err := json.Unmarshal(rawTopicDesc, &topicDesc); err != nil {
		t.Fatalf("failed to unmarshal topic description: %v", err)
	}
	if topicDesc.Description != "Beschreibung Kunstgeschichte" {
		t.Errorf("expected 'Beschreibung Kunstgeschichte', got '%s'", topicDesc.Description)
	}
}

func TestGetTopicsTool_FromTopicsFolderWithEstateType(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()

	pagesFS := fstest.MapFS{
		"pages/Topics/educational_resources.md": &fstest.MapFile{
			Data: []byte(`---
Type: estate
Title: Bildungsangebote
Shortdescription: Bildungsangebote der Sammlung
---
Markdown-Inhalt für Bildungsangebote.`),
		},
		"pages/Topics/gegenwartskunstundkultur.md": &fstest.MapFile{
			Data: []byte(`---
Type: estate
Title: Gegenwartskunst und -kultur
---
Markdown-Inhalt für Gegenwartskunst und -kultur.`),
		},
		"pages/Topics/graduation.md": &fstest.MapFile{
			Data: []byte(`---
Type: estate
Title: Graduation
---
Markdown-Inhalt für Graduation.`),
		},
		"pages/estates/ACTPerformanceFestival.md": &fstest.MapFile{
			Data: []byte(`---
Type: estate
Title: ACT Performance Festival
---
Markdown-Inhalt für ACT Performance Festival.`),
		},
	}

	ctrl := &Controller{
		name:       "test",
		pagesFS:    pagesFS,
		templateFS: fstest.MapFS{},
		staticFS:   fstest.MapFS{},
	}

	if err := ctrl.init(); err != nil {
		t.Fatalf("ctrl.init() failed: %v", err)
	}

	ctrl.initMCP(router)

	ts := httptest.NewServer(router)
	defer ts.Close()

	clientTransport := &mcp.StreamableClientTransport{
		Endpoint: ts.URL + "/mcp",
	}
	client := mcp.NewClient(&mcp.Implementation{
		Name:    "test client",
		Version: "0.0.1",
	}, nil)

	session, err := client.Connect(t.Context(), clientTransport, nil)
	if err != nil {
		t.Fatalf("failed to connect via StreamableHTTP: %v", err)
	}
	defer session.Close()

	// 1. Call get_topics and verify topics from Topics/ folder are discovered
	res, err := session.CallTool(t.Context(), &mcp.CallToolParams{
		Name: "get_topics",
	})
	if err != nil {
		t.Fatalf("CallTool get_topics failed: %v", err)
	}
	if res.IsError {
		t.Fatalf("CallTool get_topics returned error")
	}

	raw, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatalf("failed to marshal structured content: %v", err)
	}
	var resultTopics GetTopicsResult
	if err := json.Unmarshal(raw, &resultTopics); err != nil {
		t.Fatalf("failed to unmarshal structured content: %v", err)
	}

	if len(resultTopics.Topics) != 3 {
		t.Fatalf("expected 3 discovered topics from Topics folder, got %d", len(resultTopics.Topics))
	}

	expectedTitles := []string{
		"Bildungsangebote",
		"Gegenwartskunst und -kultur",
		"Graduation",
	}
	for i, expected := range expectedTitles {
		if resultTopics.Topics[i].Title != expected {
			t.Errorf("expected topic[%d].Title = %q, got %q", i, expected, resultTopics.Topics[i].Title)
		}
		if resultTopics.Topics[i].Id != int64(i+1) {
			t.Errorf("expected topic[%d].Id = %d, got %d", i, i+1, resultTopics.Topics[i].Id)
		}
	}

	// 2. Call get_topic_description by title
	descTitleRes, err := session.CallTool(t.Context(), &mcp.CallToolParams{
		Name: "get_topic_description",
		Arguments: map[string]any{
			"title": "Bildungsangebote",
		},
	})
	if err != nil {
		t.Fatalf("CallTool get_topic_description by title failed: %v", err)
	}
	if descTitleRes.IsError {
		t.Fatalf("CallTool get_topic_description returned error")
	}
	var descTitle GetTopicDescriptionResult
	rawDescTitle, _ := json.Marshal(descTitleRes.StructuredContent)
	if err := json.Unmarshal(rawDescTitle, &descTitle); err != nil {
		t.Fatalf("failed to unmarshal description: %v", err)
	}
	if descTitle.Description != "Markdown-Inhalt für Bildungsangebote." {
		t.Errorf("expected 'Markdown-Inhalt für Bildungsangebote.', got '%s'", descTitle.Description)
	}

	// 3. Call get_topic_description by ID
	descIDRes, err := session.CallTool(t.Context(), &mcp.CallToolParams{
		Name: "get_topic_description",
		Arguments: map[string]any{
			"id": 1,
		},
	})
	if err != nil {
		t.Fatalf("CallTool get_topic_description by id failed: %v", err)
	}
	if descIDRes.IsError {
		t.Fatalf("CallTool get_topic_description returned error")
	}
	var descID GetTopicDescriptionResult
	rawDescID, _ := json.Marshal(descIDRes.StructuredContent)
	if err := json.Unmarshal(rawDescID, &descID); err != nil {
		t.Fatalf("failed to unmarshal description: %v", err)
	}
	if descID.Description != "Markdown-Inhalt für Bildungsangebote." {
		t.Errorf("expected 'Markdown-Inhalt für Bildungsangebote.', got '%s'", descID.Description)
	}

	// 4. Verify estate file in estates/ is not mixed into topics
	estatesRes, err := session.CallTool(t.Context(), &mcp.CallToolParams{
		Name: "get_estates",
	})
	if err != nil {
		t.Fatalf("CallTool get_estates failed: %v", err)
	}
	var resultEstates GetEstatesResult
	rawEstates, _ := json.Marshal(estatesRes.StructuredContent)
	if err := json.Unmarshal(rawEstates, &resultEstates); err != nil {
		t.Fatalf("failed to unmarshal estates: %v", err)
	}
	if len(resultEstates.Estates) != 1 || resultEstates.Estates[0].Title != "ACT Performance Festival" {
		t.Errorf("expected 1 estate 'ACT Performance Festival', got %+v", resultEstates.Estates)
	}
}

func TestMCPResourceTemplates(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()

	ctrl := &Controller{
		name: "test",
	}
	ctrl.initMCP(router)

	ts := httptest.NewServer(router)
	defer ts.Close()

	clientTransport := &mcp.StreamableClientTransport{
		Endpoint: ts.URL + "/mcp",
	}
	client := mcp.NewClient(&mcp.Implementation{
		Name:    "test client",
		Version: "0.0.1",
	}, nil)

	session, err := client.Connect(t.Context(), clientTransport, nil)
	if err != nil {
		t.Fatalf("failed to connect via StreamableHTTP: %v", err)
	}
	defer session.Close()

	templatesList, err := session.ListResourceTemplates(t.Context(), &mcp.ListResourceTemplatesParams{})
	if err != nil {
		t.Fatalf("failed to list resource templates: %v", err)
	}

	var foundColl, foundEstate, foundTopic bool
	for _, tpl := range templatesList.ResourceTemplates {
		if tpl.URITemplate == "collection://{title}" {
			foundColl = true
			if tpl.MIMEType != "text/markdown" {
				t.Errorf("expected collection template MIMEType 'text/markdown', got %q", tpl.MIMEType)
			}
			if tpl.Name != "collection_description" {
				t.Errorf("expected collection template Name 'collection_description', got %q", tpl.Name)
			}
		}
		if tpl.URITemplate == "estate://{title}" {
			foundEstate = true
			if tpl.MIMEType != "text/markdown" {
				t.Errorf("expected estate template MIMEType 'text/markdown', got %q", tpl.MIMEType)
			}
			if tpl.Name != "estate_description" {
				t.Errorf("expected estate template Name 'estate_description', got %q", tpl.Name)
			}
		}
		if tpl.URITemplate == "topic://{title}" {
			foundTopic = true
			if tpl.MIMEType != "text/markdown" {
				t.Errorf("expected topic template MIMEType 'text/markdown', got %q", tpl.MIMEType)
			}
			if tpl.Name != "topic_description" {
				t.Errorf("expected topic template Name 'topic_description', got %q", tpl.Name)
			}
		}
	}

	if !foundColl {
		t.Errorf("resource template 'collection://{title}' not found")
	}
	if !foundEstate {
		t.Errorf("resource template 'estate://{title}' not found")
	}
	if !foundTopic {
		t.Errorf("resource template 'topic://{title}' not found")
	}
}

func TestMCPResourcesRead(t *testing.T) {
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
		"pages/estate1.md": &fstest.MapFile{
			Data: []byte(`---
type: estate
estatetitle: Test Estate 1
title: Test Estate 1
estateid: 10
---
Dies ist die Beschreibung des ersten Nachlasses.`),
		},
		"pages/topic1.md": &fstest.MapFile{
			Data: []byte(`---
type: topic
topictitle: Test Topic 1
title: Test Topic 1
topicid: 30
---
Dies ist die Beschreibung des ersten Themas.`),
		},
	}

	collections := []*CollFacetType{
		{
			Id:    1,
			Title: "Test Collection 1",
		},
	}
	estates := []*CollFacetType{
		{
			Id:    10,
			Title: "Test Estate 1",
		},
	}
	topics := []*CollFacetType{
		{
			Id:    30,
			Title: "Test Topic 1",
		},
	}

	markdowns := map[string]map[string]string{
		"collection.test collection 1": {
			"type":            "collection",
			"collectiontitle": "Test Collection 1",
			"title":           "Test Collection 1",
			"path":            "pages/coll1.md",
		},
		"estate.test estate 1": {
			"type":        "estate",
			"estatetitle": "Test Estate 1",
			"title":       "Test Estate 1",
			"estateid":    "10",
			"path":        "pages/estate1.md",
		},
		"topic.test topic 1": {
			"type":       "topic",
			"topictitle": "Test Topic 1",
			"title":      "Test Topic 1",
			"topicid":    "30",
			"path":       "pages/topic1.md",
		},
	}

	ctrl := &Controller{
		name:        "test",
		collections: collections,
		estates:     estates,
		topics:      topics,
		markdowns:   markdowns,
		pagesFS:     pagesFS,
	}

	ctrl.initMCP(router)

	ts := httptest.NewServer(router)
	defer ts.Close()

	clientTransport := &mcp.StreamableClientTransport{
		Endpoint: ts.URL + "/mcp",
	}
	client := mcp.NewClient(&mcp.Implementation{
		Name:    "test client",
		Version: "0.0.1",
	}, nil)

	session, err := client.Connect(t.Context(), clientTransport, nil)
	if err != nil {
		t.Fatalf("failed to connect via StreamableHTTP: %v", err)
	}
	defer session.Close()

	// 1. Read Collection with URL encoded title
	resColl, err := session.ReadResource(t.Context(), &mcp.ReadResourceParams{
		URI: "collection://Test%20Collection%201",
	})
	if err != nil {
		t.Fatalf("failed to read collection resource: %v", err)
	}
	if len(resColl.Contents) == 0 {
		t.Fatalf("expected contents in ReadResourceResult")
	}
	if resColl.Contents[0].MIMEType != "text/markdown" {
		t.Errorf("expected MIMEType 'text/markdown', got %q", resColl.Contents[0].MIMEType)
	}
	if resColl.Contents[0].Text != "Dies ist die Beschreibung der ersten Sammlung." {
		t.Errorf("expected text 'Dies ist die Beschreibung der ersten Sammlung.', got %q", resColl.Contents[0].Text)
	}

	// 2. Read Estate with URL encoded title
	resEstate, err := session.ReadResource(t.Context(), &mcp.ReadResourceParams{
		URI: "estate://Test%20Estate%201",
	})
	if err != nil {
		t.Fatalf("failed to read estate resource: %v", err)
	}
	if len(resEstate.Contents) == 0 {
		t.Fatalf("expected contents in ReadResourceResult")
	}
	if resEstate.Contents[0].MIMEType != "text/markdown" {
		t.Errorf("expected MIMEType 'text/markdown', got %q", resEstate.Contents[0].MIMEType)
	}
	if resEstate.Contents[0].Text != "Dies ist die Beschreibung des ersten Nachlasses." {
		t.Errorf("expected text 'Dies ist die Beschreibung des ersten Nachlasses.', got %q", resEstate.Contents[0].Text)
	}

	// 3. Read Topic with URL encoded title
	resTopic, err := session.ReadResource(t.Context(), &mcp.ReadResourceParams{
		URI: "topic://Test%20Topic%201",
	})
	if err != nil {
		t.Fatalf("failed to read topic resource: %v", err)
	}
	if len(resTopic.Contents) == 0 {
		t.Fatalf("expected contents in ReadResourceResult")
	}
	if resTopic.Contents[0].MIMEType != "text/markdown" {
		t.Errorf("expected MIMEType 'text/markdown', got %q", resTopic.Contents[0].MIMEType)
	}
	if resTopic.Contents[0].Text != "Dies ist die Beschreibung des ersten Themas." {
		t.Errorf("expected text 'Dies ist die Beschreibung des ersten Themas.', got %q", resTopic.Contents[0].Text)
	}

	// 4. Read Non-existent Collection (expect error)
	_, err = session.ReadResource(t.Context(), &mcp.ReadResourceParams{
		URI: "collection://NonExistent",
	})
	if err == nil {
		t.Fatalf("expected error reading nonexistent collection, got nil")
	}

	// 5. Read Non-existent Estate (expect error)
	_, err = session.ReadResource(t.Context(), &mcp.ReadResourceParams{
		URI: "estate://NonExistent",
	})
	if err == nil {
		t.Fatalf("expected error reading nonexistent estate, got nil")
	}

	// 6. Read Non-existent Topic (expect error)
	_, err = session.ReadResource(t.Context(), &mcp.ReadResourceParams{
		URI: "topic://NonExistent",
	})
	if err == nil {
		t.Fatalf("expected error reading nonexistent topic, got nil")
	}
}

func TestMCPToolsList_OutputSchema(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()

	ctrl := &Controller{
		name: "test",
	}
	ctrl.initMCP(router)

	ts := httptest.NewServer(router)
	defer ts.Close()

	clientTransport := &mcp.StreamableClientTransport{
		Endpoint: ts.URL + "/mcp",
	}
	client := mcp.NewClient(&mcp.Implementation{
		Name:    "test client",
		Version: "0.0.1",
	}, nil)

	session, err := client.Connect(t.Context(), clientTransport, nil)
	if err != nil {
		t.Fatalf("failed to connect via StreamableHTTP: %v", err)
	}
	defer session.Close()

	toolsList, err := session.ListTools(t.Context(), &mcp.ListToolsParams{})
	if err != nil {
		t.Fatalf("failed to list tools: %v", err)
	}

	expectedTools := []string{
		"get_collections",
		"get_collection_description",
		"get_estates",
		"get_estate_description",
		"get_topics",
		"get_topic_description",
	}

	toolsMap := make(map[string]*mcp.Tool)
	for _, tool := range toolsList.Tools {
		toolsMap[tool.Name] = tool
	}

	for _, name := range expectedTools {
		tool, ok := toolsMap[name]
		if !ok {
			t.Errorf("tool %q missing from tools list", name)
			continue
		}
		assertOutputSchemaObject(t, tool)
		assertNoTypeArrays(t, toMap(t, tool.InputSchema), name+".inputSchema")
		assertNoTypeArrays(t, toMap(t, tool.OutputSchema), name+".outputSchema")
	}

	// Verify get_collections outputSchema properties structure and anyOf
	collTool := toolsMap["get_collections"]
	collSchema := toMap(t, collTool.OutputSchema)
	collProps, ok := collSchema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("get_collections outputSchema missing properties: %+v", collSchema)
	}
	collsField, ok := collProps["collections"].(map[string]any)
	if !ok {
		t.Fatalf("get_collections outputSchema missing collections field: %+v", collProps)
	}
	assertAnyOfTypes(t, collsField, "get_collections.outputSchema.properties.collections", "null", "array")

	collItems, ok := collsField["items"].(map[string]any)
	if !ok {
		t.Fatalf("get_collections collections field missing items: %+v", collsField)
	}
	assertAnyOfTypes(t, collItems, "get_collections.outputSchema.properties.collections.items", "null", "object")

	// Verify get_estates outputSchema properties structure and anyOf
	estateTool := toolsMap["get_estates"]
	estateSchema := toMap(t, estateTool.OutputSchema)
	estateProps, ok := estateSchema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("get_estates outputSchema missing properties: %+v", estateSchema)
	}
	estatesField, ok := estateProps["estates"].(map[string]any)
	if !ok {
		t.Fatalf("get_estates outputSchema missing estates field: %+v", estateProps)
	}
	assertAnyOfTypes(t, estatesField, "get_estates.outputSchema.properties.estates", "null", "array")

	estateItems, ok := estatesField["items"].(map[string]any)
	if !ok {
		t.Fatalf("get_estates estates field missing items: %+v", estatesField)
	}
	assertAnyOfTypes(t, estateItems, "get_estates.outputSchema.properties.estates.items", "null", "object")

	// Verify get_topics outputSchema properties structure and anyOf
	topicTool := toolsMap["get_topics"]
	topicSchema := toMap(t, topicTool.OutputSchema)
	topicProps, ok := topicSchema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("get_topics outputSchema missing properties: %+v", topicSchema)
	}
	topicsField, ok := topicProps["topics"].(map[string]any)
	if !ok {
		t.Fatalf("get_topics outputSchema missing topics field: %+v", topicProps)
	}
	assertAnyOfTypes(t, topicsField, "get_topics.outputSchema.properties.topics", "null", "array")

	topicItems, ok := topicsField["items"].(map[string]any)
	if !ok {
		t.Fatalf("get_topics topics field missing items: %+v", topicsField)
	}
	assertAnyOfTypes(t, topicItems, "get_topics.outputSchema.properties.topics.items", "null", "object")
}
