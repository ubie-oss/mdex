package converter

import (
	"bytes"
	"path/filepath"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	east "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"
)

// Block represents a Notion block object.
type Block map[string]interface{}

// Convert parses Markdown content and converts it to Notion Block objects.
// mdFilePath is the path of the markdown file, used to resolve relative image paths.
// imageBaseDir is the base directory for resolving absolute image paths (e.g., Hugo's static/ directory).
// If imageBaseDir is empty, absolute paths are resolved relative to the markdown file's directory.
func Convert(content []byte, mdFilePath string, imageBaseDir string) ([]Block, error) {
	md := goldmark.New(
		goldmark.WithExtensions(
			extension.GFM,
			extension.TaskList,
		),
		goldmark.WithParserOptions(
			parser.WithAutoHeadingID(),
		),
	)

	reader := text.NewReader(content)
	doc := md.Parser().Parse(reader)

	var blocks []Block
	for child := doc.FirstChild(); child != nil; child = child.NextSibling() {
		converted := convertNode(child, content, mdFilePath, imageBaseDir)
		blocks = append(blocks, converted...)
	}

	return blocks, nil
}

func convertNode(n ast.Node, source []byte, mdFilePath string, imageBaseDir string) []Block {
	switch v := n.(type) {
	case *ast.Heading:
		return convertHeading(v, source)
	case *ast.Paragraph:
		return convertParagraph(v, source, mdFilePath, imageBaseDir)
	case *ast.FencedCodeBlock:
		return convertFencedCodeBlock(v, source)
	case *ast.CodeBlock:
		return convertCodeBlock(v, source)
	case *ast.Blockquote:
		return convertBlockquote(v, source, mdFilePath, imageBaseDir)
	case *ast.List:
		return convertList(v, source, mdFilePath, imageBaseDir)
	case *ast.ThematicBreak:
		return []Block{{"type": "divider", "divider": map[string]interface{}{}}}
	case *east.Table:
		return convertTable(v, source)
	default:
		// For unknown block types, try converting children
		var blocks []Block
		for child := n.FirstChild(); child != nil; child = child.NextSibling() {
			blocks = append(blocks, convertNode(child, source, mdFilePath, imageBaseDir)...)
		}
		return blocks
	}
}

func convertHeading(n *ast.Heading, source []byte) []Block {
	richTexts := splitRichText(convertInlineChildren(n, source))
	var headingType string
	switch n.Level {
	case 1:
		headingType = "heading_1"
	case 2:
		headingType = "heading_2"
	case 3:
		headingType = "heading_3"
	default:
		headingType = "heading_3"
	}

	return []Block{{
		"type": headingType,
		headingType: map[string]interface{}{
			"rich_text": richTexts,
		},
	}}
}

func convertParagraph(n *ast.Paragraph, source []byte, mdFilePath string, imageBaseDir string) []Block {
	// Check if paragraph contains only an image
	if n.ChildCount() == 1 {
		if img, ok := n.FirstChild().(*ast.Image); ok {
			return convertImage(img, source, mdFilePath, imageBaseDir)
		}
	}

	richTexts := splitRichText(convertInlineChildren(n, source))
	if len(richTexts) == 0 {
		return nil
	}

	return []Block{{
		"type": "paragraph",
		"paragraph": map[string]interface{}{
			"rich_text": richTexts,
		},
	}}
}

func convertImage(n *ast.Image, source []byte, mdFilePath string, imageBaseDir string) []Block {
	dest := string(n.Destination)

	// Determine if external URL or local file
	if strings.HasPrefix(dest, "http://") || strings.HasPrefix(dest, "https://") {
		return []Block{{
			"type": "image",
			"image": map[string]interface{}{
				"type": "external",
				"external": map[string]interface{}{
					"url": dest,
				},
			},
		}}
	}

	// Local image path resolution
	var localPath string
	var baseDir string
	if imageBaseDir != "" && strings.HasPrefix(dest, "/") {
		// Absolute path with imageBaseDir: resolve relative to imageBaseDir
		baseDir = filepath.Clean(imageBaseDir)
		localPath = filepath.Join(imageBaseDir, dest)
	} else {
		// Relative path or no imageBaseDir: resolve relative to the markdown file's directory
		baseDir = filepath.Clean(filepath.Dir(mdFilePath))
		localPath = filepath.Join(baseDir, dest)
	}
	localPath = filepath.Clean(localPath)

	// Prevent path traversal: ensure resolved path stays within the base directory
	if !strings.HasPrefix(localPath, baseDir+string(filepath.Separator)) && localPath != baseDir {
		return nil
	}

	// Store local path for later upload processing
	return []Block{{
		"type": "image",
		"image": map[string]interface{}{
			"type":       "file_upload",
			"local_path": localPath,
		},
	}}
}

func convertFencedCodeBlock(n *ast.FencedCodeBlock, source []byte) []Block {
	var buf bytes.Buffer
	lines := n.Lines()
	for i := 0; i < lines.Len(); i++ {
		line := lines.At(i)
		buf.Write(line.Value(source))
	}
	code := strings.TrimRight(buf.String(), "\n")

	lang := string(n.Language(source))
	if lang == "" {
		lang = "plain text"
	}

	return []Block{{
		"type": "code",
		"code": map[string]interface{}{
			"rich_text": splitRichText([]RichText{{
				Type:        "text",
				Text:        &TextContent{Content: code},
				Annotations: &RichTextAnnotation{Color: "default"},
			}}),
			"language": mapLanguage(lang),
		},
	}}
}

func convertCodeBlock(n *ast.CodeBlock, source []byte) []Block {
	var buf bytes.Buffer
	lines := n.Lines()
	for i := 0; i < lines.Len(); i++ {
		line := lines.At(i)
		buf.Write(line.Value(source))
	}
	code := strings.TrimRight(buf.String(), "\n")

	return []Block{{
		"type": "code",
		"code": map[string]interface{}{
			"rich_text": splitRichText([]RichText{{
				Type:        "text",
				Text:        &TextContent{Content: code},
				Annotations: &RichTextAnnotation{Color: "default"},
			}}),
			"language": "plain text",
		},
	}}
}

func convertBlockquote(n *ast.Blockquote, source []byte, mdFilePath string, imageBaseDir string) []Block {
	// Collect rich text from all paragraph children
	var richTexts []RichText
	for child := n.FirstChild(); child != nil; child = child.NextSibling() {
		if p, ok := child.(*ast.Paragraph); ok {
			richTexts = append(richTexts, convertInlineChildren(p, source)...)
		}
	}

	return []Block{{
		"type": "quote",
		"quote": map[string]interface{}{
			"rich_text": splitRichText(richTexts),
		},
	}}
}

func convertList(n *ast.List, source []byte, mdFilePath string, imageBaseDir string) []Block {
	var blocks []Block
	for child := n.FirstChild(); child != nil; child = child.NextSibling() {
		listItem, ok := child.(*ast.ListItem)
		if !ok {
			continue
		}
		blocks = append(blocks, convertListItem(listItem, n, source, mdFilePath, imageBaseDir)...)
	}
	return blocks
}

func convertListItem(item *ast.ListItem, list *ast.List, source []byte, mdFilePath string, imageBaseDir string) []Block {
	// Collect rich text from the first paragraph/text block
	var richTexts []RichText
	var childBlocks []Block

	for child := item.FirstChild(); child != nil; child = child.NextSibling() {
		switch v := child.(type) {
		case *ast.TextBlock:
			richTexts = append(richTexts, convertInlineChildren(v, source)...)
		case *ast.Paragraph:
			if len(richTexts) == 0 {
				richTexts = append(richTexts, convertInlineChildren(v, source)...)
			} else {
				childBlocks = append(childBlocks, convertNode(v, source, mdFilePath, imageBaseDir)...)
			}
		case *ast.List:
			childBlocks = append(childBlocks, convertList(v, source, mdFilePath, imageBaseDir)...)
		default:
			childBlocks = append(childBlocks, convertNode(v, source, mdFilePath, imageBaseDir)...)
		}
	}

	// Detect task list item by checking for TaskCheckBox child
	isTaskList := false
	checked := false
	for child := item.FirstChild(); child != nil; child = child.NextSibling() {
		if tb, ok := child.(*ast.TextBlock); ok {
			for c := tb.FirstChild(); c != nil; c = c.NextSibling() {
				if tcb, ok := c.(*east.TaskCheckBox); ok {
					isTaskList = true
					checked = tcb.IsChecked
					break
				}
			}
		}
	}

	if isTaskList {
		block := Block{
			"type": "to_do",
			"to_do": map[string]interface{}{
				"rich_text": splitRichText(richTexts),
				"checked":   checked,
			},
		}
		if len(childBlocks) > 0 {
			block["to_do"].(map[string]interface{})["children"] = childBlocks
		}
		return []Block{block}
	}

	if list.IsOrdered() {
		block := Block{
			"type": "numbered_list_item",
			"numbered_list_item": map[string]interface{}{
				"rich_text": splitRichText(richTexts),
			},
		}
		if len(childBlocks) > 0 {
			block["numbered_list_item"].(map[string]interface{})["children"] = childBlocks
		}
		return []Block{block}
	}

	block := Block{
		"type": "bulleted_list_item",
		"bulleted_list_item": map[string]interface{}{
			"rich_text": splitRichText(richTexts),
		},
	}
	if len(childBlocks) > 0 {
		block["bulleted_list_item"].(map[string]interface{})["children"] = childBlocks
	}
	return []Block{block}
}

func convertTable(n *east.Table, source []byte) []Block {
	// Count columns from the first row
	var colCount int
	var rows []interface{}

	for child := n.FirstChild(); child != nil; child = child.NextSibling() {
		row, ok := child.(*east.TableRow)
		if !ok {
			if header, ok := child.(*east.TableHeader); ok {
				// Header row
				var cells []interface{}
				for cell := header.FirstChild(); cell != nil; cell = cell.NextSibling() {
					richTexts := convertInlineChildren(cell, source)
					cells = append(cells, splitRichText(richTexts))
				}
				colCount = len(cells)
				rows = append(rows, map[string]interface{}{
					"type": "table_row",
					"table_row": map[string]interface{}{
						"cells": cells,
					},
				})
				continue
			}
			continue
		}

		var cells []interface{}
		for cell := row.FirstChild(); cell != nil; cell = cell.NextSibling() {
			richTexts := convertInlineChildren(cell, source)
			cells = append(cells, splitRichText(richTexts))
		}
		if len(cells) > colCount {
			colCount = len(cells)
		}
		rows = append(rows, map[string]interface{}{
			"type": "table_row",
			"table_row": map[string]interface{}{
				"cells": cells,
			},
		})
	}

	if colCount == 0 {
		return nil
	}

	return []Block{{
		"type": "table",
		"table": map[string]interface{}{
			"table_width":       colCount,
			"has_column_header": true,
			"has_row_header":    false,
			"children":          rows,
		},
	}}
}

// mapLanguage maps common language identifiers to Notion's supported language values.
func mapLanguage(lang string) string {
	langMap := map[string]string{
		"go":         "go",
		"golang":     "go",
		"python":     "python",
		"py":         "python",
		"javascript": "javascript",
		"js":         "javascript",
		"typescript": "typescript",
		"ts":         "typescript",
		"java":       "java",
		"c":          "c",
		"cpp":        "c++",
		"c++":        "c++",
		"csharp":     "c#",
		"cs":         "c#",
		"ruby":       "ruby",
		"rb":         "ruby",
		"rust":       "rust",
		"rs":         "rust",
		"php":        "php",
		"swift":      "swift",
		"kotlin":     "kotlin",
		"scala":      "scala",
		"shell":      "shell",
		"bash":       "shell",
		"sh":         "shell",
		"zsh":        "shell",
		"fish":       "shell",
		"sql":        "sql",
		"html":       "html",
		"css":        "css",
		"json":       "json",
		"yaml":       "yaml",
		"yml":        "yaml",
		"xml":        "xml",
		"markdown":   "markdown",
		"md":         "markdown",
		"toml":       "toml",
		"dockerfile": "docker",
		"docker":     "docker",
		"makefile":   "makefile",
		"make":       "makefile",
		"plaintext":  "plain text",
		"text":       "plain text",
		"txt":        "plain text",
		"hcl":        "hcl",
		"terraform":  "hcl",
		"tf":         "hcl",
		"graphql":    "graphql",
		"r":          "r",
		"elixir":     "elixir",
		"erlang":     "erlang",
		"haskell":    "haskell",
		"lua":        "lua",
		"perl":       "perl",
		"dart":       "dart",
		"scss":       "scss",
		"sass":       "sass",
		"less":       "less",
		"diff":       "diff",
	}

	if mapped, ok := langMap[strings.ToLower(lang)]; ok {
		return mapped
	}
	return "plain text"
}
