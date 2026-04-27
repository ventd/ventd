package hwdb

// damerauLevenshtein computes the Damerau-Levenshtein distance between a and b
// using the optimal string alignment (restricted edit) variant. This catches
// single-character transpositions (e.g. "ilo4_ulnocked" → "ilo4_unlocked") as
// well as insertions, deletions, and substitutions, each with cost 1.
func damerauLevenshtein(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	la, lb := len(ra), len(rb)
	if la == 0 {
		return lb
	}
	if lb == 0 {
		return la
	}

	dp := make([][]int, la+1)
	for i := range dp {
		dp[i] = make([]int, lb+1)
	}
	for i := 0; i <= la; i++ {
		dp[i][0] = i
	}
	for j := 0; j <= lb; j++ {
		dp[0][j] = j
	}

	for i := 1; i <= la; i++ {
		for j := 1; j <= lb; j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			dp[i][j] = min(dp[i-1][j]+1, min(dp[i][j-1]+1, dp[i-1][j-1]+cost))
			if i > 1 && j > 1 && ra[i-1] == rb[j-2] && ra[i-2] == rb[j-1] {
				dp[i][j] = min(dp[i][j], dp[i-2][j-2]+cost)
			}
		}
	}
	return dp[la][lb]
}
