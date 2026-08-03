package main

import "testing"

// TestWatchOperatorLabelsCoverEveryRegistryOperator pins the relationship
// between watchOperatorLabels (display-only) and the watchFields registry in
// watches-match.go (the single source of truth for which operators exist).
// Nothing at compile time keeps the two in sync, so this test exists to turn
// a silent drift into a failing build: add an operator to a field without
// labelling it, or leave a label behind after removing the operator that used
// it, and this test names the offending key.
func TestWatchOperatorLabelsCoverEveryRegistryOperator(t *testing.T) {
	fields := watchFieldList()

	used := map[string]bool{}
	for _, f := range fields {
		for _, op := range f.Operators {
			used[op] = true
		}
	}

	t.Run("every registry operator has a label", func(t *testing.T) {
		for _, f := range fields {
			for _, op := range f.Operators {
				label, ok := watchOperatorLabels[op]
				if !ok {
					t.Errorf("field %q uses operator %q, which has no entry in watchOperatorLabels", f.Key, op)
					continue
				}
				if label == "" {
					t.Errorf("field %q uses operator %q, which has an empty label in watchOperatorLabels", f.Key, op)
				}
			}
		}
	})

	t.Run("every label maps to an operator actually used by a field", func(t *testing.T) {
		for op := range watchOperatorLabels {
			if !used[op] {
				t.Errorf("watchOperatorLabels has an entry for %q, but no field in watchFieldList() uses it", op)
			}
		}
	})
}
