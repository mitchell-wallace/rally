package store

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
)

// appendJSONL marshals a record to JSON and appends it as a new line to the file.
func appendJSONL(path string, record any) error {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("open %s for append: %w", path, err)
	}
	defer f.Close()

	data, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("marshal record: %w", err)
	}

	if _, err := f.Write(data); err != nil {
		return fmt.Errorf("write record to %s: %w", path, err)
	}
	if _, err := f.WriteString("\n"); err != nil {
		return fmt.Errorf("write newline to %s: %w", path, err)
	}
	return nil
}

// readJSONL reads all lines from a JSONL file and unmarshals each into type T.
func readJSONL[T any](path string) ([]T, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()

	var records []T
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var rec T
		if err := json.Unmarshal(line, &rec); err != nil {
			return nil, fmt.Errorf("unmarshal line in %s: %w", path, err)
		}
		records = append(records, rec)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan %s: %w", path, err)
	}
	return records, nil
}

// rewriteJSONL overwrites a JSONL file with all provided records.
func rewriteJSONL[T any](path string, records []T) error {
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create %s: %w", path, err)
	}
	defer f.Close()

	for _, rec := range records {
		data, err := json.Marshal(rec)
		if err != nil {
			return fmt.Errorf("marshal record: %w", err)
		}
		if _, err := f.Write(data); err != nil {
			return fmt.Errorf("write record to %s: %w", path, err)
		}
		if _, err := f.WriteString("\n"); err != nil {
			return fmt.Errorf("write newline to %s: %w", path, err)
		}
	}
	return nil
}
