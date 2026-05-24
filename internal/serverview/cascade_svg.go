package serverview

import (
	"bytes"
	"fmt"
	"html"
	"html/template"
	"sort"
)

// RenderCascadeSVG consumes a CascadeGraph and returns deterministic SVG markup.
func RenderCascadeSVG(graph CascadeGraph) (template.HTML, error) {
	if len(graph.Nodes) == 0 {
		return template.HTML("<svg viewBox='0 0 100 100'></svg>"), nil
	}

	layout := computeLayout(graph)
	svg, err := renderSVG(layout, graph)
	if err != nil {
		return "", err
	}

	return template.HTML(svg), nil
}

// nodePosition holds computed x, y coordinates for a node.
type nodePosition struct {
	nodeID string
	x, y   float64
	width  float64
	height float64
}

// computeLayout assigns deterministic x, y coordinates to each node.
func computeLayout(graph CascadeGraph) []nodePosition {
	// Build adjacency.
	childMap := make(map[string][]string)
	for _, edge := range graph.Edges {
		if edge.Kind == "hierarchy" {
			childMap[edge.SourceID] = append(childMap[edge.SourceID], edge.TargetID)
		}
	}

	for _, children := range childMap {
		sort.Strings(children)
	}

	// Find roots.
	parentMap := make(map[string]string)
	for _, edge := range graph.Edges {
		if edge.Kind == "hierarchy" {
			parentMap[edge.TargetID] = edge.SourceID
		}
	}

	roots := make([]string, 0)
	for _, node := range graph.Nodes {
		if _, hasParent := parentMap[node.ID]; !hasParent {
			roots = append(roots, node.ID)
		}
	}
	sort.Strings(roots)

	positions := make([]nodePosition, 0, len(graph.Nodes))

	const (
		nodeWidth  = 200.0
		nodeHeight = 60.0
		depthGap   = 120.0
		siblingGap = 40.0
	)

	depthXPos := make(map[int]float64)

	var visit func(id string, depth int)
	visit = func(id string, depth int) {
		x := depthXPos[depth]
		depthXPos[depth] = x + nodeWidth + siblingGap
		y := float64(depth) * depthGap

		positions = append(positions, nodePosition{
			nodeID: id,
			x:      x,
			y:      y,
			width:  nodeWidth,
			height: nodeHeight,
		})

		for _, childID := range childMap[id] {
			visit(childID, depth+1)
		}
	}

	for _, root := range roots {
		visit(root, 0)
	}

	sort.Slice(positions, func(i, j int) bool {
		return positions[i].nodeID < positions[j].nodeID
	})

	return positions
}

// renderSVG generates the final SVG markup.
func renderSVG(layout []nodePosition, graph CascadeGraph) (string, error) {
	var maxX, maxY float64
	for _, pos := range layout {
		if x := pos.x + pos.width; x > maxX {
			maxX = x
		}
		if y := pos.y + pos.height; y > maxY {
			maxY = y
		}
	}

	const padding = 20.0
	viewBoxWidth := maxX + padding
	viewBoxHeight := maxY + padding

	posMap := make(map[string]nodePosition)
	for _, pos := range layout {
		posMap[pos.nodeID] = pos
	}

	var buf bytes.Buffer

	fmt.Fprintf(&buf, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %.0f %.0f" width="%.0f" height="%.0f" style="display: block; max-width: none; border: 1px solid var(--bg-hover, #ccc);">`, viewBoxWidth, viewBoxHeight, viewBoxWidth, viewBoxHeight)

	// Render edges.
	for _, edge := range graph.Edges {
		srcPos, srcOk := posMap[edge.SourceID]
		dstPos, dstOk := posMap[edge.TargetID]
		if !srcOk || !dstOk {
			continue
		}

		x1 := srcPos.x + srcPos.width/2
		y1 := srcPos.y + srcPos.height/2
		x2 := dstPos.x + dstPos.width/2
		y2 := dstPos.y + dstPos.height/2

		stroke := "gray"
		if edge.Kind == "backlink" {
			stroke = "red"
		}

		fmt.Fprintf(&buf, `<line x1="%.1f" y1="%.1f" x2="%.1f" y2="%.1f" stroke="%s" stroke-width="1" opacity="0.6"/>`, x1, y1, x2, y2, stroke)
	}

	// Render nodes. Long ids and titles are truncated with an ellipsis so
	// neighbouring nodes do not collide; the full text is preserved in a
	// <title> child so the browser shows it on hover.
	const maxIDChars = 32
	const maxTitleChars = 36
	for _, node := range graph.Nodes {
		pos, ok := posMap[node.ID]
		if !ok {
			continue
		}

		bgColor := stateColor(node.State)
		fmt.Fprintf(&buf, `<rect x="%.1f" y="%.1f" width="%.1f" height="%.1f" fill="%s" stroke="#888" stroke-width="1" rx="4"/>`,
			pos.x, pos.y, pos.width, pos.height, bgColor)

		idDisplay := truncateForSVG(node.ID, maxIDChars)
		titleDisplay := truncateForSVG(node.Title, maxTitleChars)

		fmt.Fprintf(&buf, `<text x="%.1f" y="%.1f" font-size="10" font-family="monospace" text-anchor="middle" dominant-baseline="middle" fill="#1c1e28">%s<title>%s</title></text>`,
			pos.x+pos.width/2, pos.y+18, htmlEscape(idDisplay), htmlEscape(node.ID))

		fmt.Fprintf(&buf, `<text x="%.1f" y="%.1f" font-size="11" font-family="sans-serif" text-anchor="middle" dominant-baseline="middle" fill="#1c1e28">%s<title>%s</title></text>`,
			pos.x+pos.width/2, pos.y+40, htmlEscape(titleDisplay), htmlEscape(node.Title))
	}

	fmt.Fprintf(&buf, `</svg>`)

	return buf.String(), nil
}

// stateColor returns a background color based on node state.
func stateColor(state string) string {
	switch state {
	case "todo":
		return "#fff9e6" // light yellow
	case "in_progress":
		return "#e6f3ff" // light blue
	case "complete":
		return "#e6ffe6" // light green
	case "failed":
		return "#ffe6e6" // light red
	default:
		return "#f0f0f0" // light gray
	}
}

// htmlEscape escapes HTML special characters for safe embedding in SVG text.
func htmlEscape(s string) string {
	return html.EscapeString(s)
}

// truncateForSVG shortens s to maxChars, replacing the tail with U+2026
// when truncation occurs. Counts runes (not bytes) so multi-byte text is
// not split mid-codepoint.
func truncateForSVG(s string, maxChars int) string {
	r := []rune(s)
	if len(r) <= maxChars {
		return s
	}
	if maxChars <= 1 {
		return "…"
	}
	return string(r[:maxChars-1]) + "…"
}
