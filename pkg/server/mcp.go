package server

// npx @modelcontextprotocol/inspector --server-url https://localhost:8445/mcp/ --transport http

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
	"slices"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/jsonschema-go/jsonschema"
	"github.com/je4/revcat/v2/tools/client"
	"github.com/je4/zsearch/v2/pkg/translate"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"golang.org/x/text/language"
)

type GetCollectionDescriptionArgs struct {
	Title string `json:"title,omitzero"`
	Id    int64  `json:"id,omitzero"`
}

type GetCollectionResult struct {
	Id    int64  `json:"id"`
	Title string `json:"title"`
}

type GetCollectionsResult struct {
	Collections []*GetCollectionResult `json:"collections"`
}

type GetCollectionDescriptionResult struct {
	Description string `json:"description"`
}

type GetEstateDescriptionArgs struct {
	Title string `json:"title,omitzero"`
	Id    int64  `json:"id,omitzero"`
}

type GetEstateResult struct {
	Id    int64  `json:"id"`
	Title string `json:"title"`
}

type GetEstatesResult struct {
	Estates []*GetEstateResult `json:"estates"`
}

type GetEstateDescriptionResult struct {
	Description string `json:"description"`
}

type GetTopicDescriptionArgs struct {
	Title string `json:"title,omitzero"`
	Id    int64  `json:"id,omitzero"`
}

type GetTopicResult struct {
	Id    int64  `json:"id"`
	Title string `json:"title"`
}

type GetTopicsResult struct {
	Topics []*GetTopicResult `json:"topics"`
}

type GetTopicDescriptionResult struct {
	Description string `json:"description"`
}

type GetCategoriesArgs struct{}

type GetCategoryResult struct {
	Name  string `json:"name"`
	Count int64  `json:"count"`
}

type GetCategoriesResult struct {
	Categories []*GetCategoryResult `json:"categories"`
}

type SearchArgs struct {
	Query       string   `json:"query,omitzero"`
	Search      string   `json:"search,omitzero"`
	Collections []string `json:"collections,omitzero"`
	Topics      []string `json:"topics,omitzero"`
	Estates     []string `json:"estates,omitzero"`
	From        int64    `json:"from,omitzero"`
	PageSize    int64    `json:"pageSize,omitzero"`
	Cursor      string   `json:"cursor,omitzero"`
}

type SearchItemResult struct {
	Signature string   `json:"signature"`
	Title     string   `json:"title"`
	Persons   []string `json:"persons,omitzero"`
	Date      string   `json:"date,omitzero"`
	Type      string   `json:"type,omitzero"`
	Thumbnail string   `json:"thumbnail,omitzero"`
}

type SearchPageInfoResult struct {
	HasNextPage bool   `json:"hasNextPage"`
	EndCursor   string `json:"endCursor,omitzero"`
}

type SearchResult struct {
	TotalCount int64                 `json:"totalCount"`
	PageInfo   *SearchPageInfoResult `json:"pageInfo,omitzero"`
	Items      []*SearchItemResult   `json:"items"`
	Url        string                `json:"url,omitzero"`
}

type DetailArgs struct {
	Signature string `json:"signature,omitzero"`
	Id        string `json:"id,omitzero"`
	Lang      string `json:"lang,omitzero"`
}

type DetailPersonResult struct {
	Name string  `json:"name"`
	Role *string `json:"role,omitzero"`
	Year *int64  `json:"year,omitzero"`
}

type DetailMediaResult struct {
	Name     string `json:"name"`
	MimeType string `json:"mimetype,omitzero"`
	Type     string `json:"type,omitzero"`
	Uri      string `json:"uri,omitzero"`
}

type DetailReferenceResult struct {
	Signature string `json:"signature"`
	Title     string `json:"title,omitzero"`
	Type      string `json:"type,omitzero"`
}

type DetailResult struct {
	Signature       string                   `json:"signature"`
	Title           string                   `json:"title"`
	CollectionTitle string                   `json:"collectionTitle,omitzero"`
	Source          string                   `json:"source,omitzero"`
	Abstract        string                   `json:"abstract,omitzero"`
	Persons         []*DetailPersonResult    `json:"persons,omitzero"`
	Date            string                   `json:"date,omitzero"`
	Series          string                   `json:"series,omitzero"`
	Place           string                   `json:"place,omitzero"`
	Publisher       string                   `json:"publisher,omitzero"`
	Rights          string                   `json:"rights,omitzero"`
	License         string                   `json:"license,omitzero"`
	Type            string                   `json:"type,omitzero"`
	Categories      []string                 `json:"categories,omitzero"`
	Tags            []string                 `json:"tags,omitzero"`
	Url             string                   `json:"url,omitzero"`
	Poster          string                   `json:"poster,omitzero"`
	Media           []*DetailMediaResult     `json:"media,omitzero"`
	Notes           []string                 `json:"notes,omitzero"`
	References      []*DetailReferenceResult `json:"references,omitzero"`
	Extra           map[string]string        `json:"extra,omitzero"`
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

func (ctrl *Controller) getTopicDescription(title string, id int64) (string, error) {
	return ctrl.getItemDescription("topic", ctrl.getTopics(), title, id)
}

// resolveFacetValuesByTitle matches titles against facet items strictly by title (case-insensitive)
// and extracts values grouped by prefix (e.g. "cat", "catalog", "voc", "tags").
func (ctrl *Controller) resolveFacetValuesByTitle(titles []string, items []*CollFacetType) map[string][]string {
	res, _ := ctrl.resolveFacetItemsByTitle(titles, items)
	return res
}

func (ctrl *Controller) resolveFacetItemsByTitle(titles []string, items []*CollFacetType) (map[string][]string, []*CollFacetType) {
	if len(titles) == 0 {
		return nil, nil
	}
	result := make(map[string][]string)
	var matchedItems []*CollFacetType
	for _, title := range titles {
		trimmedTitle := strings.TrimSpace(title)
		if trimmedTitle == "" {
			continue
		}
		for _, item := range items {
			if item == nil {
				continue
			}
			if strings.EqualFold(strings.TrimSpace(item.Title), trimmedTitle) {
				matchedItems = append(matchedItems, item)
				parts := strings.SplitN(item.Identifier, ":", 2)
				if len(parts) == 2 {
					prefix := strings.ToLower(strings.TrimSpace(parts[0]))
					val := strings.Trim(parts[1], "\" ")
					result[prefix] = append(result[prefix], val)
				} else if item.Identifier != "" {
					result[""] = append(result[""], strings.Trim(item.Identifier, "\" "))
				} else if item.Id != 0 {
					result[""] = append(result[""], strconv.FormatInt(item.Id, 10))
				}
				break
			}
		}
	}
	return result, matchedItems
}

func (ctrl *Controller) search(ctx context.Context, args SearchArgs) (*SearchResult, string, error) {
	searchString := cmp.Or(args.Query, args.Search)
	filterStrings, queryString, err := parseQuery(searchString)
	if err != nil {
		if ctrl.logger != nil {
			ctrl.logger.Error().Err(err).Msgf("cannot parse query '%s'", searchString)
		}
		queryString = searchString
	}

	var selectedCategoryValues []string
	var selectedCatalogValues []string
	var selectedVocabularyValues []string
	var hasCategoryFilter, hasCatalogFilter bool
	var selectedCollectionIDs []string
	var selectedCatalogIDs []string

	if len(args.Collections) > 0 {
		hasCategoryFilter = true
		mapped, matched := ctrl.resolveFacetItemsByTitle(args.Collections, ctrl.getCollections())
		if len(mapped) == 0 {
			selectedCategoryValues = append(selectedCategoryValues, "__non_existent_collection__")
		} else {
			for prefix, vals := range mapped {
				switch prefix {
				case "catalog":
					hasCatalogFilter = true
					selectedCatalogValues = append(selectedCatalogValues, vals...)
				case "voc", "tags":
					selectedVocabularyValues = append(selectedVocabularyValues, vals...)
				default:
					selectedCategoryValues = append(selectedCategoryValues, vals...)
				}
			}
			for _, item := range matched {
				if item != nil && item.Id != 0 {
					idStr := strconv.FormatInt(item.Id, 10)
					if strings.HasPrefix(item.Identifier, "catalog:") {
						if !slices.Contains(selectedCatalogIDs, idStr) {
							selectedCatalogIDs = append(selectedCatalogIDs, idStr)
						}
					} else {
						if !slices.Contains(selectedCollectionIDs, idStr) {
							selectedCollectionIDs = append(selectedCollectionIDs, idStr)
						}
					}
				}
			}
		}
	}

	if len(args.Estates) > 0 {
		estateItems := append([]*CollFacetType{}, ctrl.getEstates()...)
		for _, cat := range ctrl.catalogs {
			if cat != nil {
				estateItems = append(estateItems, cat)
			}
		}
		mapped, matched := ctrl.resolveFacetItemsByTitle(args.Estates, estateItems)
		if len(mapped) == 0 {
			hasCategoryFilter = true
			hasCatalogFilter = true
			selectedCategoryValues = append(selectedCategoryValues, "__non_existent_estate__")
			selectedCatalogValues = append(selectedCatalogValues, "__non_existent_estate__")
		} else {
			for prefix, vals := range mapped {
				switch prefix {
				case "catalog":
					hasCatalogFilter = true
					selectedCatalogValues = append(selectedCatalogValues, vals...)
				case "voc", "tags":
					selectedVocabularyValues = append(selectedVocabularyValues, vals...)
				default:
					hasCategoryFilter = true
					selectedCategoryValues = append(selectedCategoryValues, vals...)
				}
			}
			for _, item := range matched {
				if item != nil && item.Id != 0 {
					idStr := strconv.FormatInt(item.Id, 10)
					if strings.HasPrefix(item.Identifier, "cat:") {
						if !slices.Contains(selectedCollectionIDs, idStr) {
							selectedCollectionIDs = append(selectedCollectionIDs, idStr)
						}
					} else {
						if !slices.Contains(selectedCatalogIDs, idStr) {
							selectedCatalogIDs = append(selectedCatalogIDs, idStr)
						}
					}
				}
			}
		}
	}

	if len(args.Topics) > 0 {
		mapped, matched := ctrl.resolveFacetItemsByTitle(args.Topics, ctrl.getTopics())
		if len(mapped) == 0 {
			selectedVocabularyValues = append(selectedVocabularyValues, "__non_existent_topic__")
		} else {
			for prefix, vals := range mapped {
				switch prefix {
				case "catalog":
					hasCatalogFilter = true
					selectedCatalogValues = append(selectedCatalogValues, vals...)
				case "cat":
					hasCategoryFilter = true
					selectedCategoryValues = append(selectedCategoryValues, vals...)
				default:
					selectedVocabularyValues = append(selectedVocabularyValues, vals...)
				}
			}
			for _, item := range matched {
				if item != nil && item.Id != 0 {
					idStr := strconv.FormatInt(item.Id, 10)
					if strings.HasPrefix(item.Identifier, "cat:") {
						if !slices.Contains(selectedCollectionIDs, idStr) {
							selectedCollectionIDs = append(selectedCollectionIDs, idStr)
						}
					} else if strings.HasPrefix(item.Identifier, "catalog:") {
						if !slices.Contains(selectedCatalogIDs, idStr) {
							selectedCatalogIDs = append(selectedCatalogIDs, idStr)
						}
					}
				}
			}
		}
	}

	createFacet := func(name, field string, ctrlList []*CollFacetType, selectedVals []string, isFiltered bool, idPrefix string) *client.InFacet {
		facet := &client.InFacet{
			Term: &client.InFacetTerm{
				Name:        name,
				Field:       field,
				Size:        200,
				MinDocCount: 0,
				Include:     []string{},
				Exclude:     []string{},
			},
		}

		if name != "collections" && !isFiltered {
			facet.Query = &client.InFilter{
				ExistsTerm: &client.InFilterExistsTerm{
					Field: "signature",
				},
			}
		} else {
			facet.Query = &client.InFilter{
				BoolTerm: &client.InFilterBoolTerm{
					Field:  field,
					Values: []string{},
					And:    false,
				},
			}
		}

		for _, item := range ctrlList {
			if item == nil {
				continue
			}
			parts := strings.SplitN(item.Identifier, ":", 2)
			if len(parts) != 2 {
				continue
			}
			val := strings.Trim(parts[1], "\" ")
			facet.Term.Include = append(facet.Term.Include, val)
			if !isFiltered {
				if facet.Query.BoolTerm != nil {
					if parts[0] == idPrefix {
						facet.Query.BoolTerm.Values = append(facet.Query.BoolTerm.Values, val)
					}
				}
			} else if slices.Contains(selectedVals, val) {
				if facet.Query.BoolTerm != nil {
					if parts[0] == idPrefix {
						facet.Query.BoolTerm.Values = append(facet.Query.BoolTerm.Values, val)
					}
				}
			}
		}

		if isFiltered && facet.Query.BoolTerm != nil {
			for _, sv := range selectedVals {
				if !slices.Contains(facet.Query.BoolTerm.Values, sv) {
					facet.Query.BoolTerm.Values = append(facet.Query.BoolTerm.Values, sv)
				}
			}
		}

		return facet
	}

	collFacet := createFacet("collections", "category.keyword", ctrl.getCollections(), selectedCategoryValues, hasCategoryFilter, "cat")
	catFacet := createFacet("catalogs", "catalog.keyword", ctrl.catalogs, selectedCatalogValues, hasCatalogFilter, "catalog")
	mediaFacet := createFacet("medias", "mediatype.keyword", ctrl.medias, nil, false, "mediatypes.keyword")

	vocFacet := &client.InFacet{
		Term: &client.InFacetTerm{
			Name:        "vocabulary",
			Field:       "tags.keyword",
			Size:        1200,
			MinDocCount: 1,
			Include:     []string{},
			Exclude:     []string{},
		},
		Query: &client.InFilter{
			BoolTerm: &client.InFilterBoolTerm{
				Field:  "tags.keyword",
				Values: selectedVocabularyValues,
				And:    true,
			},
		},
	}
	if len(ctrl.facetInclude) > 0 {
		vocFacet.Term.Include = append(vocFacet.Term.Include, ctrl.facetInclude...)
	}
	if len(ctrl.facetExclude) > 0 {
		vocFacet.Term.Exclude = append(vocFacet.Term.Exclude, ctrl.facetExclude...)
	}

	filter := append([]*client.InFilter{}, ctrl.baseFilter...)
	if len(filterStrings) > 0 {
		for field, value := range filterStrings {
			internalField, ok := ctrl.fieldMapping[field]
			if !ok {
				if ctrl.logger != nil {
					ctrl.logger.Error().Msgf("unknown field '%s'", field)
				}
				return nil, "", fmt.Errorf("unknown field '%s'", field)
			}
			filter = append(filter, &client.InFilter{
				BoolTerm: &client.InFilterBoolTerm{
					Field:  internalField,
					Values: []string{strings.Trim(value, "\" ")},
					And:    true,
				},
			})
		}
	}

	var fromPtr, pageSizePtr *int64
	var cursorPtr *string
	if args.Cursor != "" {
		cursorPtr = &args.Cursor
	} else {
		fromVal := args.From
		pageSizeVal := args.PageSize
		if pageSizeVal <= 0 {
			pageSizeVal = 36
		}
		fromPtr = &fromVal
		pageSizePtr = &pageSizeVal
	}

	if ctrl.client == nil {
		return nil, "", errors.New("graphql client not configured")
	}

	result, err := ctrl.client.Search(ctx, "", queryString, []*client.InFacet{collFacet, catFacet, mediaFacet, vocFacet}, filter, nil, fromPtr, pageSizePtr, cursorPtr, nil)
	if err != nil {
		if ctrl.logger != nil {
			ctrl.logger.Error().Err(err).Msgf("cannot search for '%s'", searchString)
		}
		return nil, "", fmt.Errorf("search failed: %w", err)
	}

	var gridURL string
	gridBase := ctrl.searchAddr
	if gridBase == "" {
		gridBase = ctrl.externalAddr
	}
	gridBase = strings.TrimRight(gridBase, "/")

	q := url.Values{}
	if searchString != "" {
		q.Set("search", searchString)
	}
	if len(selectedCollectionIDs) > 0 {
		q.Set("collections", strings.Join(selectedCollectionIDs, ","))
	}
	if len(selectedCatalogIDs) > 0 {
		q.Set("catalogs", strings.Join(selectedCatalogIDs, ","))
	}
	if len(selectedVocabularyValues) > 0 {
		var validVocab []string
		for _, v := range selectedVocabularyValues {
			if !strings.HasPrefix(v, "__non_existent_") {
				validVocab = append(validVocab, v)
			}
		}
		if len(validVocab) > 0 {
			q.Set("vocabulary", strings.Join(validVocab, ","))
		}
	}
	if args.Cursor != "" {
		q.Set("cursor", args.Cursor)
	} else {
		if args.From > 0 {
			q.Set("from", strconv.FormatInt(args.From, 10))
		}
		if args.PageSize > 0 && args.PageSize != 36 {
			q.Set("pagesize", strconv.FormatInt(args.PageSize, 10))
		}
	}

	if encoded := q.Encode(); encoded != "" {
		gridURL = fmt.Sprintf("%s/grid/de?%s", gridBase, encoded)
	} else {
		gridURL = fmt.Sprintf("%s/grid/de", gridBase)
	}

	if result == nil || result.GetSearch() == nil {
		return &SearchResult{
			TotalCount: 0,
			Items:      []*SearchItemResult{},
			Url:        gridURL,
		}, "Keine Ergebnisse gefunden.\n", nil
	}

	searchData := result.GetSearch()
	totalCount := int64(searchData.GetTotalCount())
	var pageInfo *SearchPageInfoResult
	if pi := searchData.GetPageInfo(); pi != nil {
		pageInfo = &SearchPageInfoResult{
			HasNextPage: pi.GetHasNextPage(),
			EndCursor:   pi.GetEndCursor(),
		}
	}

	items := make([]*SearchItemResult, 0, len(searchData.GetEdges()))
	var mdBuilder strings.Builder
	if totalCount == 0 || len(searchData.GetEdges()) == 0 {
		mdBuilder.WriteString("Keine Ergebnisse gefunden.\n")
	} else {
		mdBuilder.WriteString(fmt.Sprintf("### Suchergebnisse (%d Treffer)\n\n", totalCount))
		if gridURL != "" {
			mdBuilder.WriteString(fmt.Sprintf("[Ergebnisse im Web-Katalog öffnen](%s)\n\n", gridURL))
		}
	}

	for i, edge := range searchData.GetEdges() {
		if edge == nil || edge.Base == nil {
			continue
		}
		var title string
		if len(edge.Base.GetTitle()) > 0 {
			m := &translate.MultiLangString{}
			for _, t := range edge.Base.GetTitle() {
				if l, err := language.Parse(t.Lang); err == nil {
					m.Set(t.Value, l, t.Translated)
				} else {
					m.Set(t.Value, language.Und, t.Translated)
				}
			}
			title = m.String()
			if title == "" && len(edge.Base.GetTitle()) > 0 {
				title = edge.Base.GetTitle()[0].Value
			}
		}

		var persons []string
		for _, p := range edge.Base.GetPerson() {
			if p != nil && p.GetName() != "" {
				persons = append(persons, p.GetName())
			}
		}

		sig := edge.Base.Signature
		date := emptyIfNil(edge.Base.GetDate())
		typ := emptyIfNil(edge.Base.GetType())

		var thumbnail string
		if poster := edge.Base.GetPoster(); poster != nil && poster.URI != "" {
			thumbnail = ctrl.buildThumbnailURL(poster.URI)
		}

		itemResult := &SearchItemResult{
			Signature: sig,
			Title:     title,
			Persons:   persons,
			Date:      date,
			Type:      typ,
			Thumbnail: thumbnail,
		}
		items = append(items, itemResult)

		mdBuilder.WriteString(fmt.Sprintf("%d. ", i+1))
		if title != "" {
			mdBuilder.WriteString(fmt.Sprintf("**%s**\n", title))
		} else if sig != "" {
			mdBuilder.WriteString(fmt.Sprintf("**%s**\n", sig))
		} else {
			mdBuilder.WriteString("**Ohne Titel**\n")
		}

		if thumbnail != "" {
			mdBuilder.WriteString(fmt.Sprintf("   ![Thumbnail](%s)\n", thumbnail))
		}

		if sig != "" {
			mdBuilder.WriteString(fmt.Sprintf("   - **Signatur:** `%s`\n", sig))
		}
		if len(persons) > 0 {
			mdBuilder.WriteString(fmt.Sprintf("   - **Personen:** %s\n", strings.Join(persons, ", ")))
		}
		if date != "" {
			mdBuilder.WriteString(fmt.Sprintf("   - **Datum:** %s\n", date))
		}
		if typ != "" {
			mdBuilder.WriteString(fmt.Sprintf("   - **Typ:** %s\n", typ))
		}
		mdBuilder.WriteString("\n")
	}

	searchResult := &SearchResult{
		TotalCount: totalCount,
		PageInfo:   pageInfo,
		Items:      items,
		Url:        gridURL,
	}
	return searchResult, mdBuilder.String(), nil
}

func refineCategories(categories []string) []string {
	if len(categories) == 0 {
		return nil
	}
	cats := append([]string{}, categories...)
	slices.SortFunc(cats, func(a, b string) int {
		return len(b) - len(a)
	})
	var newCategories = []string{}
	for _, cat := range cats {
		isPrefix := false
		for _, newCat := range newCategories {
			if strings.HasPrefix(newCat, cat) {
				isPrefix = true
				break
			}
		}
		if !isPrefix {
			newCategories = append(newCategories, cat)
		}
	}
	return newCategories
}

func resolveMultiLang(items []*client.MultiLangFragment, lang string) string {
	if len(items) == 0 {
		return ""
	}
	m := &translate.MultiLangString{}
	for _, t := range items {
		if t == nil {
			continue
		}
		if l, err := language.Parse(t.Lang); err == nil {
			m.Set(t.Value, l, t.Translated)
		} else {
			m.Set(t.Value, language.Und, t.Translated)
		}
	}
	if lang != "" {
		if s := m.GetStr(lang); s != "" {
			return s
		}
	}
	if s := m.String(); s != "" {
		return s
	}
	for _, t := range items {
		if t != nil && t.Value != "" {
			return t.Value
		}
	}
	return ""
}

func (ctrl *Controller) buildDetailPosterURL(uri string) string {
	if uri == "" {
		return ""
	}
	matches := mediaMatch.FindStringSubmatch(uri)
	if matches == nil {
		return uri
	}
	collection := matches[1]
	signature := matches[2]
	base := strings.TrimRight(ctrl.mediaserverBase, "/")
	return fmt.Sprintf("%s/%s/%s/resize/size1024x768/formatjpeg", base, collection, signature)
}

func (ctrl *Controller) buildDetailMediaURL(uri, mediaType, mimeType string) string {
	if uri == "" {
		return ""
	}
	matches := mediaMatch.FindStringSubmatch(uri)
	if matches == nil {
		return uri
	}
	collection := matches[1]
	signature := matches[2]
	base := strings.TrimRight(ctrl.mediaserverBase, "/")

	mType := strings.ToLower(strings.TrimSpace(mediaType))
	mMime := strings.ToLower(strings.TrimSpace(mimeType))

	switch {
	case mType == "video" || strings.HasPrefix(mMime, "video/"):
		return fmt.Sprintf("%s/%s/%s$$web/master", base, collection, signature)
	case mType == "image" || mType == "poster" || mType == "photo" || mType == "picture" || strings.HasPrefix(mMime, "image/"):
		return fmt.Sprintf("%s/%s/%s/resize/size1024x768/formatjpeg", base, collection, signature)
	case mType == "pdf" || mMime == "application/pdf" || strings.Contains(mMime, "pdf"):
		return fmt.Sprintf("%s/%s/%s/master", base, collection, signature)
	default:
		return fmt.Sprintf("%s/%s/%s/master", base, collection, signature)
	}
}

func (ctrl *Controller) getDetail(ctx context.Context, args DetailArgs) (*DetailResult, string, error) {
	sig := cmp.Or(args.Signature, args.Id)
	sig = strings.TrimSpace(sig)
	if sig == "" {
		return nil, "", errors.New("signature or id must be provided")
	}

	lang := args.Lang
	if lang == "" {
		lang = language.Und.String()
	}

	if ctrl.client == nil {
		return nil, "", errors.New("graphql client not configured")
	}

	source, err := ctrl.client.MediathekEntries(ctx, []string{sig})
	if err != nil {
		if ctrl.logger != nil {
			ctrl.logger.Error().Err(err).Msgf("cannot get mediathek entry '%s'", sig)
		}
		return nil, "", fmt.Errorf("cannot get mediathek entry '%s': %w", sig, err)
	}
	if source == nil || len(source.MediathekEntries) == 0 {
		return nil, "", fmt.Errorf("mediathek entry '%s' not found", sig)
	}

	entry := source.MediathekEntries[0]
	if entry == nil || entry.Base == nil {
		return nil, "", fmt.Errorf("mediathek entry '%s' has no base data", sig)
	}

	base := entry.Base
	title := resolveMultiLang(base.GetTitle(), lang)
	abstract := resolveMultiLang(entry.GetAbstract(), lang)

	detailBase := ctrl.detailAddr
	if detailBase == "" {
		detailBase = ctrl.externalAddr
	}
	itemURL := ""
	if base.Signature != "" {
		itemURL = fmt.Sprintf("%s/detail/%s", strings.TrimRight(detailBase, "/"), url.PathEscape(base.Signature))
	}

	refinedCats := refineCategories(base.GetCategory())

	var persons []*DetailPersonResult
	for _, p := range base.GetPerson() {
		if p == nil || p.GetName() == "" {
			continue
		}
		persons = append(persons, &DetailPersonResult{
			Name: p.GetName(),
			Role: p.GetRole(),
			Year: p.GetYear(),
		})
	}

	var posterURL string
	if poster := base.GetPoster(); poster != nil && poster.URI != "" {
		posterURL = ctrl.buildDetailPosterURL(poster.URI)
	}

	var mediaResults []*DetailMediaResult
	for _, ml := range entry.GetMedia() {
		if ml == nil {
			continue
		}
		for _, item := range ml.GetItems() {
			if item == nil {
				continue
			}
			mType := cmp.Or(item.GetType(), ml.GetType())
			mediaURL := ctrl.buildDetailMediaURL(item.GetURI(), mType, item.GetMimetype())
			mediaResults = append(mediaResults, &DetailMediaResult{
				Name:     item.GetName(),
				MimeType: item.GetMimetype(),
				Type:     mType,
				Uri:      mediaURL,
			})
		}
	}

	var notes []string
	for _, note := range entry.GetNotes() {
		if note == nil {
			continue
		}
		if note.GetTitle() != nil && *note.GetTitle() != "" {
			notes = append(notes, fmt.Sprintf("%s: %s", *note.GetTitle(), note.GetText()))
		} else {
			notes = append(notes, note.GetText())
		}
	}

	var references []*DetailReferenceResult
	for _, ref := range entry.GetReferencesFull() {
		if ref == nil {
			continue
		}
		refTitle := resolveMultiLang(ref.GetTitle(), lang)
		references = append(references, &DetailReferenceResult{
			Signature: ref.GetSignature(),
			Title:     refTitle,
			Type:      emptyIfNil(ref.GetType()),
		})
	}
	if len(references) == 0 {
		for _, ref := range base.GetReferences() {
			if ref == nil {
				continue
			}
			references = append(references, &DetailReferenceResult{
				Signature: ref.GetSignature(),
				Title:     emptyIfNil(ref.GetTitle()),
				Type:      emptyIfNil(ref.GetType()),
			})
		}
	}

	var extra map[string]string
	if len(entry.GetExtra()) > 0 {
		extra = make(map[string]string)
		for _, kv := range entry.GetExtra() {
			if kv != nil && kv.GetKey() != "" {
				extra[kv.GetKey()] = kv.GetValue()
			}
		}
	}

	detailResult := &DetailResult{
		Signature:       base.Signature,
		Title:           title,
		CollectionTitle: emptyIfNil(base.GetCollectionTitle()),
		Source:          base.GetSource(),
		Abstract:        abstract,
		Persons:         persons,
		Date:            emptyIfNil(base.GetDate()),
		Series:          emptyIfNil(base.GetSeries()),
		Place:           emptyIfNil(base.GetPlace()),
		Publisher:       emptyIfNil(base.GetPublisher()),
		Rights:          emptyIfNil(base.GetRights()),
		License:         emptyIfNil(base.GetLicense()),
		Type:            emptyIfNil(base.GetType()),
		Categories:      refinedCats,
		Tags:            base.GetTags(),
		Url:             itemURL,
		Poster:          posterURL,
		Media:           mediaResults,
		Notes:           notes,
		References:      references,
		Extra:           extra,
	}

	// Build Markdown representation
	var md strings.Builder
	if itemURL != "" && title != "" {
		md.WriteString(fmt.Sprintf("### [%s](%s)\n\n", title, itemURL))
	} else if title != "" {
		md.WriteString(fmt.Sprintf("### %s\n\n", title))
	} else {
		md.WriteString(fmt.Sprintf("### Detail: %s\n\n", base.Signature))
	}

	if posterURL != "" {
		md.WriteString(fmt.Sprintf("![Poster](%s)\n\n", posterURL))
	}

	if base.Signature != "" {
		md.WriteString(fmt.Sprintf("- **Signatur:** `%s`\n", base.Signature))
	}
	if detailResult.CollectionTitle != "" {
		md.WriteString(fmt.Sprintf("- **Sammlung:** %s\n", detailResult.CollectionTitle))
	}
	if detailResult.Source != "" {
		md.WriteString(fmt.Sprintf("- **Quelle:** %s\n", detailResult.Source))
	}
	if detailResult.Date != "" {
		md.WriteString(fmt.Sprintf("- **Datum:** %s\n", detailResult.Date))
	}
	if len(persons) > 0 {
		var personStrs []string
		for _, p := range persons {
			s := p.Name
			var details []string
			if p.Role != nil && *p.Role != "" {
				details = append(details, *p.Role)
			}
			if p.Year != nil && *p.Year != 0 {
				details = append(details, fmt.Sprintf("%d", *p.Year))
			}
			if len(details) > 0 {
				s = fmt.Sprintf("%s (%s)", s, strings.Join(details, ", "))
			}
			personStrs = append(personStrs, s)
		}
		md.WriteString(fmt.Sprintf("- **Personen:** %s\n", strings.Join(personStrs, "; ")))
	}
	if detailResult.Type != "" {
		md.WriteString(fmt.Sprintf("- **Typ:** %s\n", detailResult.Type))
	}
	if detailResult.Series != "" {
		md.WriteString(fmt.Sprintf("- **Reihentitel:** %s\n", detailResult.Series))
	}
	if detailResult.Place != "" {
		md.WriteString(fmt.Sprintf("- **Ort:** %s\n", detailResult.Place))
	}
	if detailResult.Publisher != "" {
		md.WriteString(fmt.Sprintf("- **Verlag:** %s\n", detailResult.Publisher))
	}
	if detailResult.Rights != "" {
		md.WriteString(fmt.Sprintf("- **Rechte:** %s\n", detailResult.Rights))
	}
	if detailResult.License != "" {
		md.WriteString(fmt.Sprintf("- **Lizenz:** %s\n", detailResult.License))
	}
	if len(refinedCats) > 0 {
		md.WriteString(fmt.Sprintf("- **Kategorien:** %s\n", strings.Join(refinedCats, ", ")))
	}
	if len(base.GetTags()) > 0 {
		md.WriteString(fmt.Sprintf("- **Schlagwörter:** %s\n", strings.Join(base.GetTags(), ", ")))
	}
	if itemURL != "" {
		md.WriteString(fmt.Sprintf("- **Web-Ansicht:** %s\n", itemURL))
	}

	if abstract != "" {
		md.WriteString(fmt.Sprintf("\n#### Zusammenfassung / Abstract\n%s\n", abstract))
	}

	if len(mediaResults) > 0 {
		md.WriteString("\n#### Medien\n")
		for _, m := range mediaResults {
			var details []string
			if m.MimeType != "" {
				details = append(details, m.MimeType)
			}
			if m.Type != "" && m.Type != m.MimeType {
				details = append(details, m.Type)
			}
			detailStr := ""
			if len(details) > 0 {
				detailStr = fmt.Sprintf(" (%s)", strings.Join(details, ", "))
			}
			if m.Uri != "" {
				md.WriteString(fmt.Sprintf("- %s%s: %s\n", m.Name, detailStr, m.Uri))
			} else {
				md.WriteString(fmt.Sprintf("- %s%s\n", m.Name, detailStr))
			}
		}
	}

	if len(notes) > 0 {
		md.WriteString("\n#### Notizen\n")
		for _, note := range notes {
			md.WriteString(fmt.Sprintf("- %s\n", note))
		}
	}

	if len(references) > 0 {
		md.WriteString("\n#### Referenzen\n")
		for _, ref := range references {
			refType := ""
			if ref.Type != "" {
				refType = fmt.Sprintf(" (%s)", ref.Type)
			}
			if ref.Title != "" {
				md.WriteString(fmt.Sprintf("- **%s** `%s`%s\n", ref.Title, ref.Signature, refType))
			} else {
				md.WriteString(fmt.Sprintf("- `%s`%s\n", ref.Signature, refType))
			}
		}
	}

	if len(extra) > 0 {
		md.WriteString("\n#### Zusätzliche Angaben\n")
		for k, v := range extra {
			md.WriteString(fmt.Sprintf("- **%s:** %s\n", k, v))
		}
	}

	return detailResult, md.String(), nil
}

func (ctrl *Controller) getCategories(ctx context.Context) (*GetCategoriesResult, string, error) {
	if ctrl.client == nil {
		return nil, "", errors.New("graphql client not configured")
	}

	catFacet := &client.InFacet{
		Term: &client.InFacetTerm{
			Name:        "categories",
			Field:       "category.keyword",
			Size:        1000,
			MinDocCount: 1,
			Include:     []string{},
			Exclude:     []string{},
		},
		Query: &client.InFilter{
			ExistsTerm: &client.InFilterExistsTerm{
				Field: "signature",
			},
		},
	}

	filter := append([]*client.InFilter{}, ctrl.baseFilter...)
	var pageSize int64 = 0

	result, err := ctrl.client.Search(ctx, "", "", []*client.InFacet{catFacet}, filter, nil, nil, &pageSize, nil, nil)
	if err != nil {
		if ctrl.logger != nil {
			ctrl.logger.Error().Err(err).Msg("cannot get categories facet")
		}
		return nil, "", fmt.Errorf("cannot get categories: %w", err)
	}

	categories := make([]*GetCategoryResult, 0)
	if result != nil && result.GetSearch() != nil {
		for _, facet := range result.GetSearch().GetFacets() {
			if facet == nil || facet.GetName() != "categories" {
				continue
			}
			for _, val := range facet.GetValues() {
				if val == nil {
					continue
				}
				strVal := val.GetFacetValueString()
				if strVal == nil {
					continue
				}
				categories = append(categories, &GetCategoryResult{
					Name:  strVal.GetStrVal(),
					Count: strVal.GetCount(),
				})
			}
		}
	}

	var md strings.Builder
	md.WriteString(fmt.Sprintf("### Kategorien (%d gefunden)\n\n", len(categories)))
	if len(categories) == 0 {
		md.WriteString("Keine Kategorien gefunden.\n")
	} else {
		for _, cat := range categories {
			md.WriteString(fmt.Sprintf("- %s (%d Treffer)\n", cat.Name, cat.Count))
		}
	}

	return &GetCategoriesResult{Categories: categories}, strings.TrimSpace(md.String()), nil
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

func buildSearchToolDescription(fieldMapping map[string]string) string {
	knownFieldDocs := map[string]string{
		"author":     "- `author`: Urheber, Autoren, Künstler oder beteiligte Personen (z. B. `author:\"John Doe\"`, `author:Beuys`).",
		"title":      "- `title`: Werks-, Dokument- oder Objekttitel (z. B. `title:\"Performance Art\"`, `title:Konzert`).",
		"category":   "- `category`: Kategorie-Schlagwort (z. B. `category:Video`, `category:Audio`).",
		"collection": "- `collection`: Name oder Titel der Sammlung (z. B. `collection:\"Sammlung Medienkunst\"`).",
		"signature":  "- `signature`: Exakte Archiv- oder Bestandssignatur (z. B. `signature:\"MK-123\"`).",
		"abstract":   "- `abstract`: Zusammenfassung oder Abstract-Text (z. B. `abstract:\"Dokumentation\"`).",
		"fulltext":   "- `fulltext`: Volltextsuche in verknüpften PDF-Dokumenten (z. B. `fulltext:\"Vortrag\"`).",
	}

	standardOrder := []string{"author", "title", "category", "collection", "signature", "abstract", "fulltext"}

	var docLines []string
	if len(fieldMapping) > 0 {
		var keys []string
		for k := range fieldMapping {
			keys = append(keys, k)
		}
		slices.Sort(keys)

		for _, k := range keys {
			if doc, ok := knownFieldDocs[k]; ok {
				docLines = append(docLines, doc)
			} else {
				docLines = append(docLines, fmt.Sprintf("- `%s`: Filter für Feld %s (z. B. `%s:\"Wert\"`).", k, k, k))
			}
		}
	} else {
		for _, k := range standardOrder {
			docLines = append(docLines, knownFieldDocs[k])
		}
	}

	return "Sucht im Archiv- und Bibliothekskatalog nach Mediathek-Einträgen anhand von Freitext, booleschen Operatoren, Phrasen, Feldfiltern und Facetten.\n\n" +
		"QUERY SYNTAX (Parameter `query`):\n" +
		"- Freitext: Einzelne Suchbegriffe (z. B. `Performance`).\n" +
		"- Phrasensuche: Exakte Wortgruppen in doppelten Anführungszeichen (z. B. `\"Digital Art\"` oder `\"John Doe\"`).\n" +
		"- Boolesche Operatoren: `AND`, `OR`, `NOT` sowie `&&`, `||`, `!` (z. B. `Performance AND NOT Video`, `Basel OR Zurich`).\n" +
		"- Präfix-Operatoren: `+` (Muss-Bedingung) und `-` (Ausschluss-Bedingung), z. B. `+Performance -Video`.\n" +
		"- Gruppierung & Klammern: Komplexe logische Ausdrücke mit runden Klammern, z. B. `(Performance OR Konzert) AND NOT Basel`.\n" +
		"- Feld-Filter: Strukturierte Filter im Format `field:value` oder `field:\"exact value\"` (z. B. `author:\"John Doe\"`, `title:Performance`). Werden automatisch extrahiert und mit der Freitextsuche kombiniert.\n\n" +
		"UNTERSTÜTZTE FELD-FILTER (`field:value`):\n" +
		strings.Join(docLines, "\n") + "\n\n" +
		"FACETTEN-FILTER (Dedizierte Argumente):\n" +
		"- `collections`: Liste von Sammlungstiteln (exakte Titel aus dem Tool `get_collections`).\n" +
		"- `topics`: Liste von Thementiteln (exakte Titel aus dem Tool `get_topics`).\n" +
		"- `estates`: Liste von Nachlass-/Bestandstiteln (exakte Titel aus dem Tool `get_estates`).\n\n" +
		"PAGINIERUNG:\n" +
		"- `from`: 0-basierter Startindex für den Seitenaufruf (Standard: 0).\n" +
		"- `pageSize`: Anzahl der gewünschten Ergebnisse (Standard: 36).\n" +
		"- `cursor`: End-Cursor-Token aus `pageInfo.endCursor` für cursor-basierte Weiterschaltung."
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

	mcpServer.AddResourceTemplate(&mcp.ResourceTemplate{
		URITemplate: "topic://{title}",
		Name:        "topic_description",
		Title:       "Thema Markdown-Beschreibung",
		Description: "Liefert die Markdown-Beschreibung eines Themas anhand des Titels",
		MIMEType:    "text/markdown",
	}, func(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		rawTitle := strings.TrimPrefix(req.Params.URI, "topic://")
		title, err := url.PathUnescape(rawTitle)
		if err != nil {
			title = rawTitle
		}
		desc, err := ctrl.getTopicDescription(title, 0)
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
		facets := ctrl.getCollections()
		collections := make([]*GetCollectionResult, 0, len(facets))
		for _, f := range facets {
			if f == nil {
				continue
			}
			collections = append(collections, &GetCollectionResult{
				Id:    f.Id,
				Title: f.Title,
			})
		}
		return nil, &GetCollectionsResult{Collections: collections}, nil
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
		facets := ctrl.getEstates()
		estates := make([]*GetEstateResult, 0, len(facets))
		for _, f := range facets {
			if f == nil {
				continue
			}
			estates = append(estates, &GetEstateResult{
				Id:    f.Id,
				Title: f.Title,
			})
		}
		return nil, &GetEstatesResult{Estates: estates}, nil
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

	addTool(mcpServer, &mcp.Tool{
		Name:        "get_topics",
		Description: "liefert eine Liste der Themen",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args struct{}) (*mcp.CallToolResult, *GetTopicsResult, error) {
		facets := ctrl.getTopics()
		topics := make([]*GetTopicResult, 0, len(facets))
		for _, f := range facets {
			if f == nil {
				continue
			}
			topics = append(topics, &GetTopicResult{
				Id:    f.Id,
				Title: f.Title,
			})
		}
		return nil, &GetTopicsResult{Topics: topics}, nil
	})

	addTool(mcpServer, &mcp.Tool{
		Name:        "get_topic_description",
		Description: "liefert die Beschreibung eines Themas anhand des Titels oder der ID als formatierten Markdown-Text",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args GetTopicDescriptionArgs) (*mcp.CallToolResult, *GetTopicDescriptionResult, error) {
		desc, err := ctrl.getTopicDescription(args.Title, args.Id)
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
						URI:      fmt.Sprintf("topic://%s", itemRef),
						MIMEType: "text/markdown",
						Text:     desc,
					},
				},
			},
		}, &GetTopicDescriptionResult{Description: desc}, nil
	})

	addTool(mcpServer, &mcp.Tool{
		Name:        "get_categories",
		Description: "liefert eine Liste der im Datenbestand vorhandenen Kategorien anhand einer Facettenabfrage",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args GetCategoriesArgs) (*mcp.CallToolResult, *GetCategoriesResult, error) {
		res, mdText, err := ctrl.getCategories(ctx)
		if err != nil {
			return nil, nil, err
		}
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{
					Text: mdText,
				},
			},
		}, res, nil
	})

	addTool(mcpServer, &mcp.Tool{
		Name:        "search",
		Description: buildSearchToolDescription(ctrl.fieldMapping),
	}, func(ctx context.Context, req *mcp.CallToolRequest, args SearchArgs) (*mcp.CallToolResult, *SearchResult, error) {
		res, mdText, err := ctrl.search(ctx, args)
		if err != nil {
			return nil, nil, err
		}
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{
					Text: mdText,
				},
			},
		}, res, nil
	})

	addTool(mcpServer, &mcp.Tool{
		Name:        "get_search",
		Description: buildSearchToolDescription(ctrl.fieldMapping),
	}, func(ctx context.Context, req *mcp.CallToolRequest, args SearchArgs) (*mcp.CallToolResult, *SearchResult, error) {
		res, mdText, err := ctrl.search(ctx, args)
		if err != nil {
			return nil, nil, err
		}
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{
					Text: mdText,
				},
			},
		}, res, nil
	})

	addTool(mcpServer, &mcp.Tool{
		Name:        "detail",
		Description: "liefert die vollständigen Detailinformationen zu einem Mediathek-Eintrag anhand der Signatur",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args DetailArgs) (*mcp.CallToolResult, *DetailResult, error) {
		res, mdText, err := ctrl.getDetail(ctx, args)
		if err != nil {
			return nil, nil, err
		}
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{
					Text: mdText,
				},
			},
		}, res, nil
	})

	addTool(mcpServer, &mcp.Tool{
		Name:        "get_detail",
		Description: "liefert die vollständigen Detailinformationen zu einem Mediathek-Eintrag anhand der Signatur",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args DetailArgs) (*mcp.CallToolResult, *DetailResult, error) {
		res, mdText, err := ctrl.getDetail(ctx, args)
		if err != nil {
			return nil, nil, err
		}
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{
					Text: mdText,
				},
			},
		}, res, nil
	})

	streamableHandler := mcp.NewStreamableHTTPHandler(func(r *http.Request) *mcp.Server {
		return mcpServer
	}, nil)
	mcpRouter.Any("/*any", gin.WrapH(streamableHandler))
}
