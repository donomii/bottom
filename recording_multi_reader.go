package main

import "fmt"

type recordingFileCursor struct {
	index  int
	reader *sqliteRecordingReader
	events *sqliteRecordingEventCursor
}

type recordingFileEventStream struct {
	sources []*recordingFileCursor
	limit   int
}

func streamSQLiteRecordings(operation string, paths []string, filter Filter, limit int, visit func(Event) error) error {
	stream, err := openRecordingFileEventStream(operation, paths, filter, limit)
	if err != nil {
		return err
	}
	return stream.Stream(visit)
}

func openRecordingFileEventStream(operation string, paths []string, filter Filter, limit int) (*recordingFileEventStream, error) {
	if err := validateRecordingInputPaths(operation, paths); err != nil {
		return nil, err
	}
	if limit < 0 {
		return nil, fmt.Errorf("%s limit must not be negative, received %d", operation, limit)
	}
	sources, err := openRecordingFileCursors(paths, filter, limit)
	if err != nil {
		return nil, err
	}
	return &recordingFileEventStream{sources: sources, limit: limit}, nil
}

func (stream *recordingFileEventStream) Stream(visit func(Event) error) error {
	matched := 0
	for {
		selected := nextRecordingFileCursor(stream.sources)
		if selected == nil {
			break
		}
		current := selected.events.current()
		if current.validationErr != nil {
			return joinRecorderErrors(current.validationErr, stream.Close())
		}
		if err := visit(current.event); err != nil {
			return joinRecorderErrors(err, stream.Close())
		}
		matched++
		if stream.limit > 0 && matched >= stream.limit {
			break
		}
		if err := selected.events.advance(); err != nil {
			return joinRecorderErrors(err, stream.Close())
		}
	}
	return stream.Close()
}

func (stream *recordingFileEventStream) Close() error {
	if stream == nil {
		return nil
	}
	err := closeRecordingFileCursors(stream.sources)
	stream.sources = nil
	return err
}

func validateRecordingInputPaths(operation string, paths []string) error {
	if len(paths) == 0 {
		return fmt.Errorf("%s requires at least one input recording", operation)
	}
	if len(paths) > maxRecordingInputPaths {
		return fmt.Errorf("%s accepts at most %d input recordings, received %d", operation, maxRecordingInputPaths, len(paths))
	}
	for index, path := range paths {
		if path == "" {
			return fmt.Errorf("%s input recording %d must be non-empty", operation, index+1)
		}
		for earlierIndex := 0; earlierIndex < index; earlierIndex++ {
			same, err := recordingPathsReferToSameFile(paths[earlierIndex], path)
			if err != nil {
				return fmt.Errorf("%s compare input recordings %d %q and %d %q: %w",
					operation, earlierIndex+1, paths[earlierIndex], index+1, path, err)
			}
			if same {
				return fmt.Errorf("%s requires different input recordings; paths %d %q and %d %q refer to the same file",
					operation, earlierIndex+1, paths[earlierIndex], index+1, path)
			}
		}
	}
	return nil
}

func rejectRecordingOutputAliases(operation string, outputPath string, inputPaths []string) error {
	if err := validateRecordingInputPaths(operation, inputPaths); err != nil {
		return err
	}
	for _, inputPath := range inputPaths {
		if err := rejectRecordingOutputAlias(operation, outputPath, inputPath); err != nil {
			return err
		}
	}
	return nil
}

func openRecordingFileCursors(paths []string, filter Filter, limit int) ([]*recordingFileCursor, error) {
	sources := []*recordingFileCursor{}
	for index, path := range paths {
		reader, err := openSQLiteRecordingReader(path)
		if err != nil {
			return nil, joinRecorderErrors(err, closeRecordingFileCursors(sources))
		}
		events, err := reader.newEventCursor(filter, limit)
		if err != nil {
			return nil, joinRecorderErrors(err, reader.Close(), closeRecordingFileCursors(sources))
		}
		sources = append(sources, &recordingFileCursor{index: index, reader: reader, events: events})
	}
	return sources, nil
}

func closeRecordingFileCursors(sources []*recordingFileCursor) error {
	errs := []error{}
	for _, source := range sources {
		if err := source.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	return joinRecorderErrors(errs...)
}

func (source *recordingFileCursor) Close() error {
	if source == nil {
		return nil
	}
	return joinRecorderErrors(source.events.Close(), source.reader.Close())
}

func nextRecordingFileCursor(sources []*recordingFileCursor) *recordingFileCursor {
	var selected *recordingFileCursor
	for _, source := range sources {
		if source.events.current() == nil {
			continue
		}
		if selected == nil || recordingFileCursorLess(source, selected) {
			selected = source
		}
	}
	return selected
}

func recordingFileCursorLess(left *recordingFileCursor, right *recordingFileCursor) bool {
	leftEvent := left.events.current()
	rightEvent := right.events.current()
	if !leftEvent.headTime.Equal(rightEvent.headTime) {
		return leftEvent.headTime.Before(rightEvent.headTime)
	}
	if leftEvent.headSequence != rightEvent.headSequence {
		return leftEvent.headSequence < rightEvent.headSequence
	}
	if left.index != right.index {
		return left.index < right.index
	}
	return leftEvent.source.rank < rightEvent.source.rank
}
