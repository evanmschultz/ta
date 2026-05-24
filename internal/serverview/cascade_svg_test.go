package serverview_test

import (
	"strings"
	"testing"

	"github.com/evanmschultz/ta/internal/serverview"
)

func TestRenderCascadeSVG_EmptyGraph(t *testing.T) {
	graph := serverview.CascadeGraph{}
	svg, err := serverview.RenderCascadeSVG(graph)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(string(svg), "<svg") {
		t.Errorf("expected SVG output, got: %s", svg)
	}
}

func TestRenderCascadeSVG_SingleNode(t *testing.T) {
	graph := serverview.CascadeGraph{
		Nodes: []serverview.CascadeNode{
			{
				ID:    "drop_001.drop",
				Title: "Root Drop",
				Role:  "orchestrator",
				State: "complete",
				Type:  "drop",
			},
		},
		Edges: []serverview.Edge{},
	}

	svg, err := serverview.RenderCascadeSVG(graph)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	svgStr := string(svg)
	if !strings.Contains(svgStr, "<svg") {
		t.Errorf("expected SVG tag")
	}
	if !strings.Contains(svgStr, "drop_001.drop") {
		t.Errorf("expected node ID in SVG")
	}
	if !strings.Contains(svgStr, "Root Drop") {
		t.Errorf("expected node title in SVG")
	}
}

func TestRenderCascadeSVG_MultiLevelCascade_Deterministic(t *testing.T) {
	// Build a fixture: drop -> planner -> (droplet + qa_proof + qa_falsification)
	graph := serverview.CascadeGraph{
		Nodes: []serverview.CascadeNode{
			{
				ID:    "drop_001.drop",
				Title: "Root Drop",
				Role:  "orchestrator",
				State: "complete",
				Type:  "drop",
			},
			{
				ID:    "drop_001.drop.planner_l1",
				Title: "L1 Planner",
				Role:  "planner",
				State: "complete",
				Type:  "planner",
			},
			{
				ID:    "drop_001.drop.planner_l1.droplet_a1",
				Title: "Builder Droplet A1",
				Role:  "builder",
				State: "complete",
				Type:  "droplet",
			},
			{
				ID:    "drop_001.drop.planner_l1.qa_proof",
				Title: "QA Proof",
				Role:  "qa_proof",
				State: "complete",
				Type:  "qa_proof",
			},
			{
				ID:    "drop_001.drop.planner_l1.qa_falsification",
				Title: "QA Falsification",
				Role:  "qa_falsification",
				State: "complete",
				Type:  "qa_falsification",
			},
		},
		Edges: []serverview.Edge{
			{
				SourceID: "drop_001.drop",
				TargetID: "drop_001.drop.planner_l1",
				Kind:     "hierarchy",
			},
			{
				SourceID: "drop_001.drop.planner_l1",
				TargetID: "drop_001.drop.planner_l1.droplet_a1",
				Kind:     "hierarchy",
			},
			{
				SourceID: "drop_001.drop.planner_l1",
				TargetID: "drop_001.drop.planner_l1.qa_proof",
				Kind:     "hierarchy",
			},
			{
				SourceID: "drop_001.drop.planner_l1",
				TargetID: "drop_001.drop.planner_l1.qa_falsification",
				Kind:     "hierarchy",
			},
			{
				SourceID: "drop_001.drop.planner_l1",
				TargetID: "drop_001.drop.planner_l1.qa_proof",
				Kind:     "backlink",
			},
			{
				SourceID: "drop_001.drop.planner_l1",
				TargetID: "drop_001.drop.planner_l1.qa_falsification",
				Kind:     "backlink",
			},
		},
	}

	// First render.
	svg1, err := serverview.RenderCascadeSVG(graph)
	if err != nil {
		t.Fatalf("unexpected error on first render: %v", err)
	}

	// Second render with identical graph.
	svg2, err := serverview.RenderCascadeSVG(graph)
	if err != nil {
		t.Fatalf("unexpected error on second render: %v", err)
	}

	// Assert determinism: both renders must produce identical bytes.
	if svg1 != svg2 {
		t.Errorf("RenderCascadeSVG not deterministic:\nFirst:\n%s\n\nSecond:\n%s", svg1, svg2)
	}

	// Assert all nodes are in the output.
	svgStr := string(svg1)
	nodes := []string{
		"drop_001.drop",
		"drop_001.drop.planner_l1",
		"drop_001.drop.planner_l1.droplet_a1",
		"drop_001.drop.planner_l1.qa_proof",
		"drop_001.drop.planner_l1.qa_falsification",
	}
	for _, nodeID := range nodes {
		if !strings.Contains(svgStr, nodeID) {
			t.Errorf("expected node ID %q in SVG", nodeID)
		}
	}

	// Assert all node titles are in the output.
	titles := []string{
		"Root Drop",
		"L1 Planner",
		"Builder Droplet A1",
		"QA Proof",
		"QA Falsification",
	}
	for _, title := range titles {
		if !strings.Contains(svgStr, title) {
			t.Errorf("expected title %q in SVG", title)
		}
	}

	// Assert viewBox is present.
	if !strings.Contains(svgStr, "viewBox=") {
		t.Errorf("expected viewBox attribute in SVG")
	}

	// Assert edges are present.
	if !strings.Contains(svgStr, "<line") {
		t.Errorf("expected edge lines in SVG")
	}

	// Assert nodes are rendered as rectangles.
	if !strings.Contains(svgStr, "<rect") {
		t.Errorf("expected node rectangles in SVG")
	}
}

func TestRenderCascadeSVG_StateColors(t *testing.T) {
	graph := serverview.CascadeGraph{
		Nodes: []serverview.CascadeNode{
			{
				ID:    "drop_001.drop.todo",
				Title: "To Do",
				Role:  "builder",
				State: "todo",
				Type:  "droplet",
			},
			{
				ID:    "drop_001.drop.inprog",
				Title: "In Progress",
				Role:  "builder",
				State: "in_progress",
				Type:  "droplet",
			},
			{
				ID:    "drop_001.drop.complete",
				Title: "Complete",
				Role:  "builder",
				State: "complete",
				Type:  "droplet",
			},
			{
				ID:    "drop_001.drop.failed",
				Title: "Failed",
				Role:  "builder",
				State: "failed",
				Type:  "droplet",
			},
		},
		Edges: []serverview.Edge{},
	}

	svg, err := serverview.RenderCascadeSVG(graph)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	svgStr := string(svg)
	colors := []string{"#fff9e6", "#e6f3ff", "#e6ffe6", "#ffe6e6"}
	for _, color := range colors {
		if !strings.Contains(svgStr, color) {
			t.Errorf("expected color %s in SVG", color)
		}
	}
}

func TestRenderCascadeSVG_HierarchyVsBacklinkEdges(t *testing.T) {
	graph := serverview.CascadeGraph{
		Nodes: []serverview.CascadeNode{
			{
				ID:    "drop_001.drop",
				Title: "Drop",
				Role:  "orchestrator",
				State: "complete",
				Type:  "drop",
			},
			{
				ID:    "drop_001.drop.planner",
				Title: "Planner",
				Role:  "planner",
				State: "complete",
				Type:  "planner",
			},
			{
				ID:    "drop_001.drop.planner.qa",
				Title: "QA",
				Role:  "qa_proof",
				State: "complete",
				Type:  "qa_proof",
			},
		},
		Edges: []serverview.Edge{
			{
				SourceID: "drop_001.drop",
				TargetID: "drop_001.drop.planner",
				Kind:     "hierarchy",
			},
			{
				SourceID: "drop_001.drop.planner",
				TargetID: "drop_001.drop.planner.qa",
				Kind:     "backlink",
			},
		},
	}

	svg, err := serverview.RenderCascadeSVG(graph)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	svgStr := string(svg)
	if !strings.Contains(svgStr, `stroke="gray"`) {
		t.Errorf("expected gray hierarchy edge")
	}
	if !strings.Contains(svgStr, `stroke="red"`) {
		t.Errorf("expected red backlink edge")
	}
}
