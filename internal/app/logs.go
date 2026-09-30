package app

import (
	"fmt"
	"io"
	"os"
	"trestle/internal/runlog"
)

func Logs(path, id string, output io.Writer) error {
	records, err := runlog.List(path)
	if err != nil {
		return err
	}
	for _, record := range records {
		if id == "" {
			fmt.Fprintf(output, "%s  %-12s %-20s %s\n", record.ID, record.Status, record.Operation, record.LogPath)
			continue
		}
		if record.ID == id {
			file, err := os.Open(record.LogPath)
			if err != nil {
				return err
			}
			defer file.Close()
			_, err = io.Copy(output, file)
			return err
		}
	}
	if id != "" {
		return fmt.Errorf("unknown operation ID %q; use trestle logs to list operations", id)
	}
	return nil
}
