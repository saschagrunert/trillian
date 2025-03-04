// Copyright 2019 Google LLC. All Rights Reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// A binary to produce LaTeX documents representing Merkle trees.
// The generated document should be fed into xelatex, and the Forest package
// must be available.
//
// Usage: go run main.go | xelatex
// This should generate a PDF file called treetek.pdf containing a drawing of
// the tree.
//
package main

import (
	"flag"
	"fmt"
	"log"
	"math/bits"
	"strings"

	"github.com/transparency-dev/merkle/compact"
	"github.com/transparency-dev/merkle/proof"
)

const (
	preamble = `
% Hash-tree
% Author: treetex
\documentclass[convert]{standalone}
\usepackage[dvipsnames]{xcolor}
\usepackage{forest}


\begin{document}

% Change colours here:
\definecolor{proof}{rgb}{1,0.5,0.5}
\definecolor{proof_ephemeral}{rgb}{1,0.7,0.7}
\definecolor{perfect}{rgb}{1,0.9,0.5}
\definecolor{target}{rgb}{0.5,0.5,0.9}
\definecolor{target_path}{rgb}{0.7,0.7,0.9}
\definecolor{mega}{rgb}{0.9,0.9,0.9}
\definecolor{target0}{rgb}{0.1,0.9,0.1}
\definecolor{target1}{rgb}{0.1,0.1,0.9}
\definecolor{target2}{rgb}{0.9,0.1,0.9}
\definecolor{range0}{rgb}{0.3,0.9,0.3}
\definecolor{range1}{rgb}{0.3,0.3,0.9}
\definecolor{range2}{rgb}{0.9,0.3,0.9}

\forestset{
	% This defines a new "edge" style for drawing the perfect subtrees.
	% Rather than simply drawing a line representing an edge, this draws a
	% triangle between the labelled anchors on the given nodes.
	% See "Anchors" section in the Forest manual for more details:
	%  http://mirrors.ibiblio.org/CTAN/graphics/pgf/contrib/forest/forest-doc.pdf
	perfect/.style={edge path={%
		\noexpand\path[fill=mega, \forestoption{edge}]
				(.parent first)--(!u.children)--(.parent last)--cycle
				\forestoption{edge label};
		}
	},
}
\begin{forest}
`

	postfix = `\end{forest}
\end{document}
`

	// Maximum number of ranges to allow.
	maxRanges = 3
)

var (
	treeSize   = flag.Uint64("tree_size", 23, "Size of tree to produce")
	leafData   = flag.String("leaf_data", "", "Comma separated list of leaf data text (setting this overrides --tree_size")
	nodeFormat = flag.String("node_format", "address", "Format for internal node text, one of: address, hash")
	inclusion  = flag.Int64("inclusion", -1, "Leaf index to show inclusion proof")
	megaMode   = flag.Uint("megamode_threshold", 4, "Treat perfect trees larger than this many layers as a single entity")
	ranges     = flag.String("ranges", "", "Comma-separated Open-Closed ranges of the form L:R")

	attrPerfectRoot   = flag.String("attr_perfect_root", "", "Latex treatment for perfect root nodes (e.g. 'line width=3pt')")
	attrEphemeralNode = flag.String("attr_ephemeral_node", "draw, dotted", "Latex treatment for ephemeral nodes")

	// nInfo holds nodeInfo data for the tree.
	nInfo = make(map[compact.NodeID]nodeInfo)
)

// nodeInfo represents the style to be applied to a tree node.
// TODO(al): separate out leafdata bits from here.
type nodeInfo struct {
	proof            bool
	incPath          bool
	target           bool
	perfectRoot      bool
	ephemeral        bool
	leaf             bool
	dataRangeIndices []int
	rangeIndices     []int
}

type nodeTextFunc func(id compact.NodeID) string

// String returns a string containing Forest attributes suitable for
// rendering the node, given its type.
func (n nodeInfo) String() string {
	attr := make([]string, 0, 4)

	// Figure out which colour to fill with:
	fill := "white"
	if n.perfectRoot {
		attr = append(attr, *attrPerfectRoot)
	}

	if n.proof {
		fill = "proof"
		if n.ephemeral {
			fill = "proof_ephemeral"
		}
	}

	if n.leaf {
		if l := len(n.dataRangeIndices); l == 1 {
			fill = fmt.Sprintf("target%d!50", n.dataRangeIndices[0])
		} else if l > 1 {
			// Otherwise, we need to be a bit cleverer, and use the shading feature.
			for i, ri := range n.dataRangeIndices {
				pos := []string{"left", "right", "middle"}[i]
				attr = append(attr, fmt.Sprintf("%s color=target%d!50", pos, ri))
			}
		}
	} else {
		if l := len(n.rangeIndices); l == 1 {
			fill = fmt.Sprintf("range%d!50", n.rangeIndices[0])
		} else if l > 1 {
			for i, pi := range n.rangeIndices {
				pos := []string{"left", "right", "middle"}[i]
				attr = append(attr, fmt.Sprintf("%s color=range%d!50", pos, pi))
			}
		}
	}
	if n.target {
		fill = "target"
	}
	if n.incPath {
		fill = "target_path"
	}

	attr = append(attr, "fill="+fill)

	if !n.ephemeral {
		attr = append(attr, "draw")
	} else {
		attr = append(attr, *attrEphemeralNode)
	}
	if !n.leaf {
		attr = append(attr, "circle, minimum size=3em, align=center")
	} else {
		attr = append(attr, "minimum size=1.5em, align=center, base=bottom")
	}
	return strings.Join(attr, ", ")
}

// modifyNodeInfo applies f to the nodeInfo associated with node id.
func modifyNodeInfo(id compact.NodeID, f func(*nodeInfo)) {
	n := nInfo[id] // Note: Returns an empty nodeInfo if id is not found.
	f(&n)
	nInfo[id] = n
}

// perfectMega renders a large perfect subtree as a single entity.
func perfectMega(prefix string, id compact.NodeID) {
	begin, end := id.Coverage()
	size := end - begin

	stWidth := float32(size) / float32(*treeSize)
	fmt.Printf("%s [%d\\dots%d, edge label={node[midway, above]{%d}}, perfect, tier=leaf, minimum width=%f\\linewidth ]\n", prefix, begin, end, size, stWidth)

	// Create some hidden nodes to preseve the tier spacings:
	fmt.Printf("%s", prefix)
	for i := int(id.Level) - 2; i > 0; i-- {
		fmt.Printf(" [, no edge, tier=%d ", i)
		defer fmt.Printf(" ] ")
	}
}

// perfect renders a perfect subtree.
func perfect(prefix string, id compact.NodeID, nodeText, dataText nodeTextFunc) {
	perfectInner(prefix, id, true, nodeText, dataText)
}

// drawLeaf emits TeX code to render a leaf.
func drawLeaf(prefix string, index uint64, leafText, dataText nodeTextFunc) {
	id := compact.NewNodeID(0, index)
	a := nInfo[id]

	// First render the leaf node of the Merkle tree.
	if len(a.dataRangeIndices) > 0 {
		a.incPath = false
	}
	fmt.Printf("%s [%s, %s, align=center, tier=leaf\n", prefix, leafText(id), a.String())

	// and then a child-node representing the leaf data itself:
	a = nInfo[id]
	a.leaf = true
	a.proof = false                        // proofs don't include leafdata (just the leaf hash above)
	a.incPath, a.target = false, a.incPath // draw the target leaf darker if necessary.
	fmt.Printf("  %s [%s, %s, align=center, tier=leafdata]\n]\n", prefix, dataText(id), a.String())
}

// openInnerNode renders TeX code to open an internal node.
// The caller may emit any number of child nodes before calling the returned
// func to close the node.
// Returns a func to be called to close the node.
func openInnerNode(prefix string, id compact.NodeID, nodeText nodeTextFunc) func() {
	attr := nInfo[id].String()
	fmt.Printf("%s [%s, %s, tier=%d\n", prefix, nodeText(id), attr, id.Level)
	return func() { fmt.Printf("%s ]\n", prefix) }
}

// perfectInner renders the nodes of a perfect internal subtree.
func perfectInner(prefix string, id compact.NodeID, top bool, nodeText nodeTextFunc, dataText nodeTextFunc) {
	modifyNodeInfo(id, func(n *nodeInfo) {
		n.perfectRoot = top
	})

	if id.Level == 0 {
		drawLeaf(prefix, id.Index, nodeText, dataText)
		return
	}
	defer openInnerNode(prefix, id, nodeText)()

	if id.Level > *megaMode {
		perfectMega(prefix, id)
	} else {
		left := compact.NewNodeID(id.Level-1, id.Index*2)
		perfectInner(prefix+" ", left, false, nodeText, dataText)
		perfectInner(prefix+" ", left.Sibling(), false, nodeText, dataText)
	}
}

// renderTree renders a tree node and recurses if necessary.
func renderTree(prefix string, size uint64, nodeText, dataText nodeTextFunc) {
	// Get root IDs of all perfect subtrees.
	ids := compact.RangeNodes(0, size, nil)
	for i, id := range ids {
		if i+1 < len(ids) {
			ephem := id.Parent()
			modifyNodeInfo(ephem, func(n *nodeInfo) { n.ephemeral = true })
			defer openInnerNode(prefix, ephem, nodeText)()
		}
		prefix += " "
		perfect(prefix, id, nodeText, dataText)
	}
}

// parseRanges parses and validates a string of comma-separates open-closed
// ranges of the form L:R.
// Returns the parsed ranges, or an error if there's a problem.
func parseRanges(ranges string, treeSize uint64) ([][2]uint64, error) {
	rangePairs := strings.Split(ranges, ",")
	numRanges := len(rangePairs)
	if num, max := numRanges, maxRanges; num > max {
		return nil, fmt.Errorf("too many ranges %d, must be %d or fewer", num, max)
	}
	ret := make([][2]uint64, 0, numRanges)
	for _, rng := range rangePairs {
		lr := strings.Split(rng, ":")
		if len(lr) != 2 {
			return nil, fmt.Errorf("specified range %q is invalid", rng)
		}
		var l, r uint64
		if _, err := fmt.Sscanf(rng, "%d:%d", &l, &r); err != nil {
			return nil, fmt.Errorf("range %q is malformed: %s", rng, err)
		}
		switch {
		case r > treeSize:
			return nil, fmt.Errorf("range %q extends past end of tree (%d)", lr, treeSize)
		case l > r:
			return nil, fmt.Errorf("range elements in %q are out of order", rng)
		}
		ret = append(ret, [2]uint64{l, r})
	}
	return ret, nil
}

// modifyRangeNodeInfo sets style info for nodes affected by ranges.
// This includes leaves and perfect subtree roots.
// TODO(al): Figure out what, if anything, to do to make this show ranges
// which are inside the perfect meganodes.
func modifyRangeNodeInfo() error {
	rng, err := parseRanges(*ranges, *treeSize)
	if err != nil {
		return err
	}
	for ri, lr := range rng {
		l, r := lr[0], lr[1]
		// Set leaves:
		for i := l; i < r; i++ {
			id := compact.NewNodeID(0, i)
			modifyNodeInfo(id, func(n *nodeInfo) {
				n.dataRangeIndices = append(n.dataRangeIndices, ri)
			})
		}

		for _, id := range compact.RangeNodes(l, r, nil) {
			modifyNodeInfo(id, func(n *nodeInfo) {
				n.rangeIndices = append(n.rangeIndices, ri)
			})
		}
	}
	return nil
}

var dataFormat = func(id compact.NodeID) string {
	return fmt.Sprintf("{$leaf_{%d}$}", id.Index)
}

var nodeFormats = map[string]nodeTextFunc{
	"address": func(id compact.NodeID) string {
		return fmt.Sprintf("%d.%d", id.Level, id.Index)
	},
	"hash": func(id compact.NodeID) string {
		// For "hash" format node text, levels >=1 need a different format
		// [H=H(childL|childR)]from the base level (H=H(leafN)].
		if id.Level >= 1 {
			childLevel := id.Level - 1
			leftChild := id.Index * 2
			return fmt.Sprintf("{$H_{%d.%d} =$ \\\\ $H(H_{%d.%d} || H_{%d.%d})$}", id.Level, id.Index, childLevel, leftChild, childLevel, leftChild+1)
		}
		return fmt.Sprintf("{$H_{%d.%d} =$ \\\\ $H(leaf_{%[2]d})$}", id.Level, id.Index)
	},
}

// Whee - here we go!
func main() {
	// TODO(al): check flag validity.
	flag.Parse()
	height := uint(bits.Len64(*treeSize-1)) + 1

	innerNodeText := nodeFormats[*nodeFormat]
	if innerNodeText == nil {
		log.Fatalf("unknown --node_format %s", *nodeFormat)
	}

	nodeText := innerNodeText

	if len(*leafData) > 0 {
		leaves := strings.Split(*leafData, ",")
		*treeSize = uint64(len(leaves))
		log.Printf("Overriding treeSize to %d since --leaf_data was set", *treeSize)
		dataFormat = func(id compact.NodeID) string {
			return leaves[id.Index]
		}
	}

	if *inclusion > 0 {
		leafID := compact.NewNodeID(0, uint64(*inclusion))
		modifyNodeInfo(leafID, func(n *nodeInfo) { n.incPath = true })
		nodes, err := proof.Inclusion(uint64(*inclusion), *treeSize)
		if err != nil {
			log.Fatalf("Failed to calculate inclusion proof addresses: %s", err)
		}
		_, begin, end := nodes.Ephem()
		for i, id := range nodes.IDs {
			// Skip children of the ephemeral node.
			if i >= begin && i < end && begin+1 < end {
				continue
			}
			modifyNodeInfo(id, func(n *nodeInfo) { n.proof = true })
		}
		// If the ephemeral node exists in the proof, make it a parent of the biggest subtree.
		if begin+1 < end {
			modifyNodeInfo(nodes.IDs[end-1].Parent(), func(n *nodeInfo) { n.proof = true })
		}

		for id := leafID; id.Level < height; id = id.Parent() {
			modifyNodeInfo(id, func(n *nodeInfo) { n.incPath = true })
		}
	}

	if len(*ranges) > 0 {
		if err := modifyRangeNodeInfo(); err != nil {
			log.Fatalf("Failed to modify range node styles: %s", err)
		}
	}

	// TODO(al): structify this into a util, and add ability to output to an
	// arbitrary stream.
	fmt.Print(preamble)
	renderTree("", *treeSize, nodeText, dataFormat)
	fmt.Print(postfix)
}
