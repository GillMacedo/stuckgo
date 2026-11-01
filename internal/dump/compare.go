package dump

import "sort"

// A stack this crowded is worth showing when its goroutines stayed put, even if it isn't growing.
const parkedAlone = 10

// Change is how one stack's count moved between two dumps.
type Change struct {
	Where  string `json:"where"`
	State  string `json:"state"`
	Before int    `json:"before"`
	After  int    `json:"after"`
	Delta  int    `json:"delta"`
}

// Parked is a set of goroutines seen in both dumps at the same place.
type Parked struct {
	Where          string `json:"where"`
	State          string `json:"state"`
	Count          int    `json:"count"`
	MaxWaitMinutes int    `json:"max_wait_minutes,omitempty"`
	Growing        bool   `json:"growing,omitempty"`

	key string
}

// Comparison is what changed between two dumps of one program.
type Comparison struct {
	Before    int      `json:"before_total"`
	After     int      `json:"after_total"`
	Growing   []Change `json:"growing"`
	Shrinking []Change `json:"shrinking"`
	Parked    []Parked `json:"parked"`
	// ParkedElsewhere counts goroutines that stayed in place at stacks that aren't growing, which long-running loops do.
	ParkedElsewhere int       `json:"parked_elsewhere"`
	New             []Finding `json:"new_findings"`
	Fixed           []Finding `json:"fixed_findings"`
	Remaining       []Finding `json:"remaining_findings"`
	Notes           []string  `json:"notes,omitempty"`
}

// Compare shows what moved between two dumps. A count that keeps growing, and
// goroutine IDs that never leave one place, are stronger evidence than one dump.
func Compare(before, after *Dump) Comparison {
	beforeReview, afterReview := Analyze(before), Analyze(after)
	result := Comparison{Before: beforeReview.Total, After: afterReview.Total, Growing: []Change{}, Shrinking: []Change{}, Parked: []Parked{}}

	type row struct {
		where, state  string
		before, after int
	}
	rows := map[string]*row{}
	var order []string
	add := func(groups []Group, assign func(*row, int)) {
		for _, group := range groups {
			if group.Expected {
				continue
			}
			key := group.State + "\n" + group.key
			item, ok := rows[key]
			if !ok {
				item = &row{where: group.Where, state: group.State}
				rows[key] = item
				order = append(order, key)
			}
			assign(item, group.Count)
		}
	}
	add(buildGroups(before), func(item *row, count int) { item.before += count })
	add(buildGroups(after), func(item *row, count int) { item.after += count })
	for _, key := range order {
		item := rows[key]
		change := Change{Where: item.where, State: item.state, Before: item.before, After: item.after, Delta: item.after - item.before}
		switch {
		case change.Delta > 0:
			result.Growing = append(result.Growing, change)
		case change.Delta < 0:
			result.Shrinking = append(result.Shrinking, change)
		}
	}
	sort.SliceStable(result.Growing, func(i, j int) bool { return result.Growing[i].Delta > result.Growing[j].Delta })
	sort.SliceStable(result.Shrinking, func(i, j int) bool { return result.Shrinking[i].Delta < result.Shrinking[j].Delta })

	if before.Format == FormatTraceback && after.Format == FormatTraceback {
		grew := map[string]bool{}
		for _, key := range order {
			grew[key] = rows[key].after > rows[key].before
		}
		for _, item := range parked(before, after) {
			item.Growing = grew[item.key]
			if item.Growing || item.Count >= parkedAlone {
				result.Parked = append(result.Parked, item)
			} else {
				result.ParkedElsewhere += item.Count
			}
		}
	} else {
		result.Notes = append(result.Notes, "Goroutine IDs need debug=2 in both dumps, so this can't show which goroutines stayed in place.")
	}
	if before.Format != after.Format {
		result.Notes = append(result.Notes, "These dumps are in different formats, so stacks may not line up exactly.")
	}
	result.Notes = append(result.Notes, "Two dumps are a sample. A goroutine can leave a place and return to it between them.")

	key := func(finding Finding) string { return finding.ID + "\n" + finding.Where }
	seenBefore, seenAfter := map[string]bool{}, map[string]bool{}
	for _, finding := range beforeReview.Findings {
		seenBefore[key(finding)] = true
	}
	for _, finding := range afterReview.Findings {
		seenAfter[key(finding)] = true
		if seenBefore[key(finding)] {
			result.Remaining = append(result.Remaining, finding)
		} else {
			result.New = append(result.New, finding)
		}
	}
	for _, finding := range beforeReview.Findings {
		if !seenAfter[key(finding)] {
			result.Fixed = append(result.Fixed, finding)
		}
	}
	return result
}

func parked(before, after *Dump) []Parked {
	earlier := map[int]string{}
	for _, g := range before.Goroutines {
		earlier[g.ID] = g.State + "\n" + stackKey(g.Frames)
	}
	groups := map[string]*Parked{}
	var order []string
	for _, g := range after.Goroutines {
		key := g.State + "\n" + stackKey(g.Frames)
		if earlier[g.ID] != key || isExpected(g, g.State) || g.State == "running" || g.State == "runnable" || g.State == "sleep" {
			continue
		}
		item, ok := groups[key]
		if !ok {
			item = &Parked{Where: where(g.Frames), State: g.State, key: key}
			groups[key] = item
			order = append(order, key)
		}
		item.Count++
		if g.WaitMinutes > item.MaxWaitMinutes {
			item.MaxWaitMinutes = g.WaitMinutes
		}
	}
	result := make([]Parked, 0, len(order))
	for _, key := range order {
		result = append(result, *groups[key])
	}
	sort.SliceStable(result, func(i, j int) bool { return result[i].Count > result[j].Count })
	return result
}
