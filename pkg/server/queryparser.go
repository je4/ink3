package server

import (
	"github.com/alecthomas/participle/v2"
	"github.com/alecthomas/participle/v2/lexer"
	"regexp"
	"strings"
)

var queryLexer = lexer.MustSimple([]lexer.SimpleRule{
	{Name: "Whitespace", Pattern: `\s+`},
	{Name: "String", Pattern: `"(\\.|[^"])*"|'(\\.|[^'])*'`},
	{Name: "And", Pattern: `(?i)AND\b|&&`},
	{Name: "Or", Pattern: `(?i)OR\b|\|\|`},
	{Name: "Not", Pattern: `(?i)NOT\b|!`},
	{Name: "Plus", Pattern: `\+`},
	{Name: "Minus", Pattern: `-`},
	{Name: "LParen", Pattern: `\(`},
	{Name: "RParen", Pattern: `\)`},
	{Name: "Colon", Pattern: `:`},
	{Name: "Ident", Pattern: `[^\s:()!|&"']+`},
})

type Property struct {
	Key   string `@Ident ":"`
	Value string `(@String | @Ident)`
}

type Group struct {
	Items []*Expression `"(" ( @@ )* ")"`
}

type Expression struct {
	Property *Property `@@`
	Group    *Group    `| @@`
	And      string    `| @And`
	Or       string    `| @Or`
	Not      string    `| @Not`
	Plus     string    `| @Plus`
	Minus    string    `| @Minus`
	Phrase   string    `| @String`
	Term     string    `| @Ident`
}

type Query struct {
	Expressions []*Expression `( @@ )*`
}

var queryParser = participle.MustBuild[Query](
	participle.Lexer(queryLexer),
	participle.Elide("Whitespace"),
	participle.UseLookahead(3),
)

func unquote(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 {
		if (s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'') {
			return s[1 : len(s)-1]
		}
	}
	return s
}

func isBinaryOp(tok string) bool {
	upper := strings.ToUpper(tok)
	return upper == "AND" || upper == "OR" || tok == "&&" || tok == "||" || tok == "|" || tok == "+"
}

func isUnaryOp(tok string) bool {
	upper := strings.ToUpper(tok)
	return upper == "NOT" || tok == "!" || tok == "+" || tok == "-"
}

func sanitizeTokens(tokens []string) []string {
	var cleaned []string
	for _, tok := range tokens {
		tok = strings.TrimSpace(tok)
		if tok == "" {
			continue
		}
		if tok == "|" {
			if len(cleaned) == 0 {
				continue
			}
			last := cleaned[len(cleaned)-1]
			if last == "|" || last == "+" || last == "-" || last == "(" {
				continue
			}
		} else if tok == "+" {
			if len(cleaned) > 0 {
				last := cleaned[len(cleaned)-1]
				if last == "+" || last == "|" || last == "-" || last == "(" {
					continue
				}
			}
		} else if tok == "-" {
			if len(cleaned) > 0 {
				last := cleaned[len(cleaned)-1]
				if last == "-" {
					continue
				}
			}
		}
		cleaned = append(cleaned, tok)
	}

	for len(cleaned) > 0 {
		last := cleaned[len(cleaned)-1]
		if last == "|" || last == "+" || last == "-" {
			cleaned = cleaned[:len(cleaned)-1]
		} else {
			break
		}
	}

	return cleaned
}

func joinTokens(tokens []string) string {
	var sb strings.Builder
	for i, tok := range tokens {
		if i > 0 {
			prev := tokens[i-1]
			if prev == "(" || tok == ")" {
				// no space around parentheses
			} else if prev == "-" {
				// unary minus attaches to following token
			} else if prev == "+" && (i == 1 || tokens[i-2] == "(") && tok != "-" {
				// unary plus at start of query or group attaches to following token
			} else {
				sb.WriteString(" ")
			}
		}
		sb.WriteString(tok)
	}
	return sb.String()
}

func extractExpressions(exprs []*Expression, filter map[string]string) []string {
	var tokens []string
	for _, expr := range exprs {
		if expr == nil {
			continue
		}
		if expr.Property != nil {
			if expr.Property.Key != "" {
				filter[expr.Property.Key] = unquote(expr.Property.Value)
			}
			continue
		}
		if expr.Group != nil {
			groupTokens := extractExpressions(expr.Group.Items, filter)
			groupTokens = sanitizeTokens(groupTokens)
			if len(groupTokens) > 0 {
				groupStr := joinTokens(groupTokens)
				if len(groupTokens) == 1 && !strings.Contains(groupStr, " ") {
					tokens = append(tokens, groupStr)
				} else {
					tokens = append(tokens, "("+groupStr+")")
				}
			}
			continue
		}
		if expr.And != "" {
			if len(tokens) > 0 {
				tokens = append(tokens, "+")
			}
		} else if expr.Or != "" {
			if len(tokens) > 0 {
				tokens = append(tokens, "|")
			}
		} else if expr.Not != "" {
			tokens = append(tokens, "-")
		} else if expr.Plus != "" {
			tokens = append(tokens, "+")
		} else if expr.Minus != "" {
			tokens = append(tokens, "-")
		} else if expr.Phrase != "" {
			tokens = append(tokens, expr.Phrase)
		} else if expr.Term != "" {
			tokens = append(tokens, expr.Term)
		}
	}
	return tokens
}

var propertyRegex = regexp.MustCompile(`\b([a-zA-Z0-9_-]+):("([^"\\]*(\\.[^"\\]*)*)"|'([^'\\]*(\\.[^'\\]*)*)'|([^\s()]+))`)

func fallbackParseQuery(query string) (map[string]string, string) {
	filter := map[string]string{}
	qResult := propertyRegex.ReplaceAllStringFunc(query, func(match string) string {
		parts := strings.SplitN(match, ":", 2)
		if len(parts) == 2 {
			filter[parts[0]] = unquote(parts[1])
			return ""
		}
		return match
	})
	qResult = strings.Join(strings.Fields(qResult), " ")
	return filter, qResult
}

func parseQuery(query string) (map[string]string, string, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return map[string]string{}, "", nil
	}

	result, err := queryParser.ParseString("", query)
	if err != nil {
		fallbackFilter, fallbackQ := fallbackParseQuery(query)
		return fallbackFilter, fallbackQ, nil
	}

	filter := map[string]string{}
	tokens := extractExpressions(result.Expressions, filter)
	tokens = sanitizeTokens(tokens)
	queryString := joinTokens(tokens)

	return filter, queryString, nil
}
