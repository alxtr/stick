package sticks

import "strconv"

const (
	maxHistoryPageLinks = 7
	pageSize            = 20
	maxHistoryOffset    = 100_000
	maxHistoryPage      = maxHistoryOffset/pageSize + 1
)

func parseHistoryPage(raw string) int {
	page, err := strconv.Atoi(raw)
	if err != nil || page < 1 {
		return 1
	}
	if page > maxHistoryPage {
		return maxHistoryPage
	}
	return page
}

// ParseHistoryPage normalizes and bounds a history page number.
func ParseHistoryPage(raw string) int { return parseHistoryPage(raw) }

func historyPageLinks(page, totalPages int) []int {
	if totalPages < 1 {
		return nil
	}
	if page > totalPages {
		page = totalPages
	}

	window := maxHistoryPageLinks
	if totalPages < window {
		window = totalPages
	}
	start := page - window/2
	if start < 1 {
		start = 1
	}
	end := start + window - 1
	if end < start || end > totalPages {
		end = totalPages
		start = end - window + 1
		if start < 1 {
			start = 1
		}
	}

	links := make([]int, end-start+1)
	for i := range links {
		links[i] = start + i
	}
	return links
}

// HistoryPageLinks returns the bounded pagination window used by stick pages.
func HistoryPageLinks(page, totalPages int) []int { return historyPageLinks(page, totalPages) }
