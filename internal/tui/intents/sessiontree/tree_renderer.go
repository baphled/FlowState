package sessiontree

import "strings"

// treeNode represents a session in the tree hierarchy with resolved children.
type treeNode struct {
	sessionID string
	agentID   string
	children  []*treeNode
}

// displayLine represents a single rendered line in the flattened tree output.
type displayLine struct {
	sessionID string
	text      string
}

// buildTree constructs a tree from a flat list of SessionNode values.
//
// Expected:
//   - nodes is a flat list of SessionNode values where exactly one has ParentID == "".
//
// Returns:
//   - The root treeNode, or nil if no root is found.
//
// Side effects:
//   - None.
func buildTree(nodes []SessionNode) *treeNode {
	if len(nodes) == 0 {
		return nil
	}

	nodeMap := make(map[string]*treeNode, len(nodes))
	for i := range nodes {
		nodeMap[nodes[i].SessionID] = &treeNode{
			sessionID: nodes[i].SessionID,
			agentID:   nodes[i].AgentID,
		}
	}

	var root *treeNode
	for i := range nodes {
		n := &nodes[i]
		tn := nodeMap[n.SessionID]
		if n.ParentID == "" {
			root = tn
		} else if parent, ok := nodeMap[n.ParentID]; ok {
			parent.children = append(parent.children, tn)
		}
	}

	return root
}

// flattenContext holds shared state for the recursive tree flattening operation.
type flattenContext struct {
	currentSessionID string
	cursorID         string
	lines            []displayLine
}

// flattenTree produces a depth-first ordered list of displayLine values
// with box-drawing connectors and indentation.
//
// Expected:
//   - root is a non-nil treeNode built by buildTree.
//   - currentSessionID identifies the session to mark with a bullet.
//   - cursorID identifies the session to mark with an angle bracket.
//
// Returns:
//   - A slice of displayLine values in depth-first order.
//
// Side effects:
//   - None.
func flattenTree(root *treeNode, currentSessionID, cursorID string) []displayLine {
	if root == nil {
		return nil
	}

	ctx := &flattenContext{
		currentSessionID: currentSessionID,
		cursorID:         cursorID,
	}
	ctx.flattenNode(root, "", true, true)
	return ctx.lines
}

// flattenNode recursively flattens a single treeNode into display lines.
//
// Expected:
//   - node is a non-nil treeNode.
//   - prefix is the indentation string for this level.
//   - isLast indicates whether this node is the last child of its parent.
//   - isRoot indicates whether this node is the tree root.
//
// Returns:
//   - Nothing; appends to ctx.lines.
//
// Side effects:
//   - Appends to the lines slice on the receiver.
func (ctx *flattenContext) flattenNode(node *treeNode, prefix string, isLast, isRoot bool) {
	label := formatNodeLabel(node, ctx.currentSessionID, ctx.cursorID)

	var line string
	if isRoot {
		line = label
	} else {
		connector := "├─ "
		if isLast {
			connector = "└─ "
		}
		line = prefix + connector + label
	}

	ctx.lines = append(ctx.lines, displayLine{
		sessionID: node.sessionID,
		text:      line,
	})

	childPrefix := prefix
	if !isRoot {
		if isLast {
			childPrefix += "   "
		} else {
			childPrefix += "│  "
		}
	}

	for i, child := range node.children {
		childIsLast := i == len(node.children)-1
		ctx.flattenNode(child, childPrefix, childIsLast, false)
	}
}

// formatNodeLabel formats the display label for a single tree node.
//
// Expected:
//   - node is a non-nil treeNode.
//   - currentSessionID identifies the session to mark with a bullet.
//   - cursorID identifies the session to mark with an angle bracket.
//
// Returns:
//   - A string like "● agentID (sessionID)" or "> agentID (sessionID)" or "agentID (sessionID)".
//
// Side effects:
//   - None.
func formatNodeLabel(node *treeNode, currentSessionID, cursorID string) string {
	base := node.agentID + " (" + node.sessionID + ")"

	isCurrent := node.sessionID == currentSessionID
	isCursor := node.sessionID == cursorID

	if isCurrent && isCursor {
		return "● " + base
	}
	if isCurrent {
		return "● " + base
	}
	if isCursor {
		return "> " + base
	}
	return base
}

// renderTree produces the full tree view string from display lines.
//
// Expected:
//   - lines is a slice of displayLine values from flattenTree.
//
// Returns:
//   - A string with one line per display entry, newline-terminated.
//
// Side effects:
//   - None.
func renderTree(lines []displayLine) string {
	var buf strings.Builder
	for _, dl := range lines {
		buf.WriteString(dl.text)
		buf.WriteByte('\n')
	}
	return buf.String()
}
