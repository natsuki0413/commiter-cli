package gitstate

import "sort"

func Collect(root string, options Options) (snapshot Snapshot, err error) {
	if options.Files == nil {
		options.Files = osFiles{}
	}
	commonDir, err := gitPath(root, "--git-common-dir")
	if err != nil {
		return Snapshot{}, internal("cannot resolve common Git directory")
	}
	lock, err := acquireLock(commonDir)
	if err != nil {
		return Snapshot{}, err
	}
	defer func() {
		if closeErr := lock.Close(); err == nil && closeErr != nil {
			err = closeErr
		}
	}()
	state, err := inspect(root)
	if err != nil {
		return Snapshot{}, err
	}

	rawChanges, err := readStatus(root, options.Pathspecs)
	if err != nil {
		return Snapshot{}, err
	}
	selected := make([]rawChange, 0, len(rawChanges))
	excluded := make([]Excluded, 0)
	candidateIndexes := make([]int, 0)
	candidates := make([]Candidate, 0)
	for _, change := range rawChanges {
		include, err := selectedByGlobs(change, options.Include, options.Exclude)
		if err != nil {
			return Snapshot{}, err
		}
		if !include {
			continue
		}
		level, reason, err := classifyChangePaths(change, options.AdditionalSensitiveGlobs)
		if err != nil {
			return Snapshot{}, err
		}
		if level == automaticallyExcluded {
			excluded = append(excluded, Excluded{Path: change.displayPath(), Reason: reason})
			continue
		}
		selected = append(selected, change)
		if level == sensitiveCandidate {
			candidateIndexes = append(candidateIndexes, len(selected)-1)
			candidates = append(candidates, Candidate{Path: change.displayPath(), Reason: reason})
		}
	}

	approved := false
	if len(candidates) > 0 && options.ApproveSensitiveCandidates != nil {
		approved, err = options.ApproveSensitiveCandidates(candidates)
		if err != nil {
			return Snapshot{}, internal("cannot obtain sensitive file approval")
		}
	}
	approvedIndexes := make(map[int]bool, len(candidateIndexes))
	for candidateOffset, selectedIndex := range candidateIndexes {
		if approved {
			approvedIndexes[selectedIndex] = true
		} else {
			excluded = append(excluded, Excluded{Path: candidates[candidateOffset].Path, Reason: "sensitive candidate was not approved"})
		}
	}

	filtered := make([]rawChange, 0, len(selected))
	filteredSensitive := make([]bool, 0, len(selected))
	for index, change := range selected {
		isCandidate := containsIndex(candidateIndexes, index)
		if isCandidate && !approvedIndexes[index] {
			continue
		}
		filtered = append(filtered, change)
		filteredSensitive = append(filteredSensitive, approvedIndexes[index])
	}
	sortRawWithSensitive(filtered, filteredSensitive)

	snapshot = Snapshot{
		Root:      root,
		Head:      state.head,
		Branch:    state.branch,
		Changes:   []Change{},
		Untracked: []string{},
		Excluded:  excluded,
	}
	if snapshot.IndexIdentity, err = indexIdentity(root); err != nil {
		return Snapshot{}, err
	}
	for index, change := range filtered {
		built, include, buildErr := buildChange(root, change, options.Files, filteredSensitive[index], state.objectFormat)
		if buildErr != nil {
			return Snapshot{}, buildErr
		}
		if !include {
			continue
		}
		built.ID = fileID(len(snapshot.Changes))
		snapshot.Changes = append(snapshot.Changes, built)
		if change.untracked {
			snapshot.Untracked = append(snapshot.Untracked, change.displayPath())
		}
	}
	sort.Strings(snapshot.Untracked)
	sort.Slice(snapshot.Excluded, func(i, j int) bool { return snapshot.Excluded[i].Path < snapshot.Excluded[j].Path })
	finalState, err := inspect(root)
	if err != nil {
		return Snapshot{}, err
	}
	finalIndex, err := indexIdentity(root)
	if err != nil {
		return Snapshot{}, err
	}
	if finalState.head != snapshot.Head || finalState.branch != snapshot.Branch || finalState.objectFormat != state.objectFormat || finalIndex != snapshot.IndexIdentity {
		return Snapshot{}, safety("Git state changed while collecting the snapshot")
	}
	return snapshot, nil
}

func classifyChangePaths(change rawChange, additional []string) (sensitivity, string, error) {
	paths := []*string{change.oldPath, change.newPath}
	result := notSensitive
	reason := ""
	for _, value := range paths {
		if value == nil {
			continue
		}
		level, pathReason, err := classifySensitive(*value, additional)
		if err != nil {
			return notSensitive, "", err
		}
		if level > result {
			result, reason = level, pathReason
		}
	}
	return result, reason, nil
}

func containsIndex(values []int, target int) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func sortRawWithSensitive(changes []rawChange, sensitive []bool) {
	type pair struct {
		change    rawChange
		sensitive bool
	}
	pairs := make([]pair, len(changes))
	for index := range changes {
		pairs[index] = pair{change: changes[index], sensitive: sensitive[index]}
	}
	sort.Slice(pairs, func(i, j int) bool {
		left := pairs[i].change.displayPath() + "\x00" + pointerValue(pairs[i].change.oldPath) + "\x00" + pairs[i].change.status
		right := pairs[j].change.displayPath() + "\x00" + pointerValue(pairs[j].change.oldPath) + "\x00" + pairs[j].change.status
		return left < right
	})
	for index := range pairs {
		changes[index] = pairs[index].change
		sensitive[index] = pairs[index].sensitive
	}
}
