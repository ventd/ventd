# RULE-EXPERIMENTAL-SCHEMA-03: Unknown experimental key with Levenshtein distance ≤ 2 from a known key is rejected as a likely typo with a suggestion.

When an `experimental:` block contains an unrecognized key whose Damerau-Levenshtein distance
to the nearest recognized key is ≤ 2, `validateExperimental` MUST return a non-nil error
containing the text `"Did you mean:"` followed by the closest recognized key. The catalog
load MUST fail. Distance ≤ 2 covers one-character substitutions, insertions, deletions, and
transpositions — the full typo space for short identifiers. Accepting such a key silently would
leave the intended feature disabled with no indication to the catalog author.

Bound: internal/hwdb/profile_v1_1_test.go:TestSchemaValidator_ExperimentalBlock_RejectsTypoWithSuggestion
