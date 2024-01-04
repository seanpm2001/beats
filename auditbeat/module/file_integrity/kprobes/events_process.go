package kprobes

import (
	"context"
	"errors"
	"golang.org/x/sys/unix"
	"path/filepath"
)

// TODO(panosk) finalise the Emitter interface according to our needs
type Emitter interface {
	Emit(ePath string, pid uint32, op uint32) error
}

type eventProcessor interface {
	process(ctx context.Context, pe *ProbeEvent) error
}

type eProcessor struct {
	p           pathTraverser
	e           Emitter
	d           *dEntryCache
	isRecursive bool
}

func newEventProcessor(p pathTraverser, e Emitter, isRecursive bool) *eProcessor {
	return &eProcessor{
		p:           p,
		e:           e,
		d:           newDirEntryCache(),
		isRecursive: isRecursive,
	}
}

func (e *eProcessor) process(ctx context.Context, pe *ProbeEvent) error {
	// after processing return the probe event to the pool
	defer releaseProbeEvent(pe)

	switch {
	case pe.MaskMonitor == 1:
		// Monitor events are only generated by our own pathTraverser.AddPathToMonitor or
		// pathTraverser.WalkAsync

		monitorPath, match := e.p.GetMonitorPath(pe.FileIno, pe.FileDevMajor, pe.FileDevMinor, pe.FileName)
		if !match {
			return nil
		}

		entry := e.d.Get(dKey{
			Ino:      pe.FileIno,
			DevMajor: pe.FileDevMajor,
			DevMinor: pe.FileDevMinor,
		})

		parentEntry := e.d.Get(dKey{
			Ino:      pe.ParentIno,
			DevMajor: pe.ParentDevMajor,
			DevMinor: pe.ParentDevMinor,
		})

		if parentEntry == nil {
			entry = &dEntry{
				Name:     monitorPath.fullPath,
				Ino:      pe.FileIno,
				Depth:    monitorPath.depth,
				DevMajor: pe.FileDevMajor,
				DevMinor: pe.FileDevMinor,
			}
		} else {
			if entry == nil {
				entry = &dEntry{
					Name:     pe.FileName,
					Ino:      pe.FileIno,
					Depth:    parentEntry.Depth + 1,
					DevMajor: pe.FileDevMajor,
					DevMinor: pe.FileDevMinor,
				}
			}
		}

		e.d.Add(entry, parentEntry)

		if !monitorPath.isFromMove {
			return nil
		}

		return e.e.Emit(entry.Path(), monitorPath.tid, unix.IN_MOVED_TO)

	case pe.MaskCreate == 1:
		parentEntry := e.d.Get(dKey{
			Ino:      pe.ParentIno,
			DevMajor: pe.ParentDevMajor,
			DevMinor: pe.ParentDevMinor,
		})

		if parentEntry == nil || parentEntry.Depth >= 1 && !e.isRecursive {
			return nil
		}

		entry := &dEntry{
			Children: nil,
			Name:     pe.FileName,
			Ino:      pe.FileIno,
			DevMajor: pe.FileDevMajor,
			DevMinor: pe.FileDevMinor,
		}

		e.d.Add(entry, parentEntry)

		return e.e.Emit(entry.Path(), pe.Meta.TID, unix.IN_CREATE)

	case pe.MaskModify == 1:
		entry := e.d.Get(dKey{
			Ino:      pe.FileIno,
			DevMajor: pe.FileDevMajor,
			DevMinor: pe.FileDevMinor,
		})

		if entry == nil {
			return nil
		}

		return e.e.Emit(entry.Path(), pe.Meta.TID, unix.IN_MODIFY)

	case pe.MaskAttrib == 1:
		entry := e.d.Get(dKey{
			Ino:      pe.FileIno,
			DevMajor: pe.FileDevMajor,
			DevMinor: pe.FileDevMinor,
		})

		if entry == nil {
			return nil
		}

		return e.e.Emit(entry.Path(), pe.Meta.TID, unix.IN_ATTRIB)

	case pe.MaskMoveFrom == 1:
		parentEntry := e.d.Get(dKey{
			Ino:      pe.ParentIno,
			DevMajor: pe.ParentDevMajor,
			DevMinor: pe.ParentDevMinor,
		})

		if parentEntry == nil || parentEntry.Depth >= 1 && !e.isRecursive {
			e.d.MoveClear(uint64(pe.Meta.TID))
			return nil
		}

		entry := parentEntry.GetChild(pe.FileName)
		if entry == nil {
			return nil
		}

		entryPath := entry.Path()

		e.d.MoveFrom(uint64(pe.Meta.TID), entry)

		return e.e.Emit(entryPath, pe.Meta.TID, unix.IN_MOVED_FROM)

	case pe.MaskMoveTo == 1:
		parentEntry := e.d.Get(dKey{
			Ino:      pe.ParentIno,
			DevMajor: pe.ParentDevMajor,
			DevMinor: pe.ParentDevMinor,
		})

		if parentEntry == nil || parentEntry.Depth >= 1 && !e.isRecursive {
			// if parentEntry is nil then this move event is not
			// for a directory we monitor
			e.d.MoveClear(uint64(pe.Meta.TID))
			return nil
		}

		moved, err := e.d.MoveTo(uint64(pe.Meta.TID), parentEntry, pe.FileName, func(path string) error {
			return e.e.Emit(path, pe.Meta.TID, unix.IN_MOVED_TO)
		})
		if err != nil {
			return err
		}
		if moved {
			return nil
		}

		newEntryPath := filepath.Join(parentEntry.Path(), pe.FileName)
		e.p.WalkAsync(newEntryPath, parentEntry.Depth+1, pe.Meta.TID)

		return nil

	case pe.MaskDelete == 1:
		parentEntry := e.d.Get(dKey{
			Ino:      pe.ParentIno,
			DevMajor: pe.ParentDevMajor,
			DevMinor: pe.ParentDevMinor,
		})

		if parentEntry == nil || parentEntry.Depth >= 1 && !e.isRecursive {
			return nil
		}

		entry := parentEntry.GetChild(pe.FileName)
		if entry == nil {
			return nil
		}

		entryPath := entry.Path()

		e.d.Remove(entry)

		if err := e.e.Emit(entryPath, pe.Meta.TID, unix.IN_DELETE); err != nil {
			return err
		}

		entry.Release()
		entry = nil

		return nil
	default:
		return errors.New("unknown event type")
	}
}
