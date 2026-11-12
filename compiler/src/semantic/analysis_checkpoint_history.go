package semantic

import "elisacore/src/checkpointhistory"

func planCheckpointHistory(history []string) ([]int, int) {
	return checkpointhistory.Plan(history, smtHistoryLimit)
}

func appendCheckpointHistory(graph []byte, history []string, prefixes []int, capacity int) []byte {
	return checkpointhistory.Append(graph, history, prefixes, capacity)
}

func splitCheckpointHistory(data []byte) ([]byte, []byte, error) {
	return checkpointhistory.Split(data)
}

func restoreCheckpointHistory(data []byte) ([]string, error) {
	return checkpointhistory.Restore(data, smtHistoryLimit)
}
