package injection

import "strings"

// MaxBlockBytes bounds one block as it goes into a request, in UTF-8 bytes:
// its opening and closing lines and the items that fit.
const MaxBlockBytes = 8 << 10

// Block is what a plugin gives for the system prompt or for a turn: an
// opening line, items and a closing line. Items let the host fit a block into
// its budget one by one instead of dropping it whole. A plain text is a block
// of one item.
type Block struct {
	Open  string
	Items []string
	Close string
}

// Render lays the block out one line each, within max bytes. Items go in in
// order; the first that does not fit and every item after it are dropped,
// and dropped counts them. A block left with no items renders as "".
func (block Block) Render(max int) (text string, dropped int) {
	var lines []string
	size, limit := 0, max
	add := func(line string) bool {
		grown := size + len(line)
		if len(lines) > 0 {
			grown++
		}
		if grown > limit {
			return false
		}
		lines = append(lines, line)
		size = grown
		return true
	}
	if block.Open != "" && !add(block.Open) {
		return "", countItems(block.Items)
	}
	// The closing line must fit after the items: hold its room back while
	// they go in.
	if block.Close != "" {
		limit -= len(block.Close) + 1
	}
	items := 0
	for index, item := range block.Items {
		if item == "" {
			continue
		}
		if !add(item) {
			dropped = countItems(block.Items[index:])
			break
		}
		items++
	}
	if items == 0 {
		return "", dropped
	}
	if block.Close != "" {
		limit = max
		add(block.Close)
	}
	return strings.Join(lines, "\n"), dropped
}

func countItems(items []string) int {
	count := 0
	for _, item := range items {
		if item != "" {
			count++
		}
	}
	return count
}
