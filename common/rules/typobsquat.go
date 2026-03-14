// SPDX-License-Identifier: Apache-2.0

// Package rules — typosquatting detection using Levenshtein distance.

package rules

import "strings"

// NormalizeName lowercases the package name and replaces hyphens, underscores,
// and dots with empty string so that "my-package", "my_package", and
// "my.package" all compare equal (PEP 503 / npm normalisation).
func NormalizeName(pkg string) string {
	var sb strings.Builder
	for _, ch := range strings.ToLower(pkg) {
		if ch == '-' || ch == '_' || ch == '.' {
			continue
		}
		sb.WriteRune(ch)
	}
	return sb.String()
}

// LevenshteinDistance returns the edit distance between a and b, using an
// early-exit optimisation: if the running minimum exceeds maxDist the function
// returns maxDist+1 immediately.
func LevenshteinDistance(a, b string, maxDist int) int {
	ra, rb := []rune(a), []rune(b)
	la, lb := len(ra), len(rb)
	if la == 0 {
		return lb
	}
	if lb == 0 {
		return la
	}
	row := make([]int, lb+1)
	for j := range row {
		row[j] = j
	}
	for i := 1; i <= la; i++ {
		prev := i
		rowMin := prev
		for j := 1; j <= lb; j++ {
			cost := row[j-1]
			if ra[i-1] != rb[j-1] {
				cost = min3(row[j]+1, prev+1, row[j-1]+1)
			}
			row[j-1] = prev
			prev = cost
			if prev < rowMin {
				rowMin = prev
			}
		}
		row[lb] = prev
		if rowMin > maxDist {
			return maxDist + 1
		}
	}
	return row[lb]
}

func min3(a, b, c int) int {
	if a < b {
		if a < c {
			return a
		}
		return c
	}
	if b < c {
		return b
	}
	return c
}

// typosquatting returns true when name is "close" (within maxDist edits) to any
// protected package but is NOT an exact match. Uses NormalizeName for comparison.
func typosquatting(name string, protected []string, maxDist int) bool {
	if maxDist <= 0 {
		maxDist = 2
	}
	n := NormalizeName(name)
	for _, p := range protected {
		np := NormalizeName(p)
		if n == np {
			return false // exact match — this IS the real package
		}
		if LevenshteinDistance(n, np, maxDist) <= maxDist {
			return true
		}
	}
	return false
}
