package api

import (
	"context"
	"sort"

	"github.com/marrasen/aprot"

	"github.com/marrasen/marraw/internal/aimask"
	"github.com/marrasen/marraw/internal/edit"
)

const modelsInfoKey = "modelsInfo"

// ModelsInfo describes the downloaded ML model weights: where they live and
// what is on disk — the Settings "Downloaded models" section.
type ModelsInfo struct {
	Dir    string      `json:"dir"`
	Models []ModelFile `json:"models"`
}

// ModelFile is one model file on disk. Name/Purpose are empty for a file no
// current spec references (weights superseded by a version bump).
type ModelFile struct {
	FileName string `json:"fileName"`
	Name     string `json:"name,omitempty"`
	Purpose  string `json:"purpose,omitempty"`
	Bytes    int64  `json:"bytes"`
}

// modelCatalog maps the pinned specs' on-disk names to what the Settings UI
// shows. Keep the entries in step with aimask's model pins.
func modelCatalog() map[string]ModelFile {
	catalog := map[string]ModelFile{}
	for kind, meta := range map[edit.AIKind]ModelFile{
		edit.AISubject: {Name: "Subject (ISNet)", Purpose: "Finds the photo's main subject, for Subject AI masks."},
		edit.AIDepth:   {Name: "Depth (Depth Anything V2 Small)", Purpose: "Estimates relative scene depth, for Depth AI masks."},
		edit.AIClass:   {Name: "Semantic classes (DPT · ADE20K)", Purpose: "Detects regions like sky, people, and foliage, for category AI masks."},
	} {
		if spec, ok := aimask.SpecFor(kind); ok {
			catalog[spec.FileName()] = meta
		}
	}
	return catalog
}

// GetModelsInfo lists the downloaded model files. Subscription query:
// DeleteModel and a model download (GenerateAIMap) push an update.
func (s *System) GetModelsInfo(ctx context.Context) (*ModelsInfo, error) {
	aprot.RegisterRefreshTrigger(ctx, modelsInfoKey)
	return s.modelsInfo()
}

func (s *System) modelsInfo() (*ModelsInfo, error) {
	if s.deps.Infer == nil {
		return &ModelsInfo{}, nil
	}
	files, err := s.deps.Infer.InstalledModels()
	if err != nil {
		return nil, err
	}
	catalog := modelCatalog()
	info := &ModelsInfo{Dir: s.deps.Infer.Dir()}
	for _, f := range files {
		mf := catalog[f.FileName] // zero value for superseded files
		mf.FileName = f.FileName
		mf.Bytes = f.Bytes
		info.Models = append(info.Models, mf)
	}
	// Largest first: the delete button exists to reclaim disk space.
	sort.Slice(info.Models, func(i, j int) bool { return info.Models[i].Bytes > info.Models[j].Bytes })
	return info, nil
}

// DeleteModel removes one downloaded model file. Safe: weights re-download
// (with the usual consent prompt) the next time a feature needs them, and
// generated maps/edits are untouched.
func (s *System) DeleteModel(ctx context.Context, fileName string) (*ModelsInfo, error) {
	if s.deps.Infer == nil {
		return nil, aprot.ErrInvalidParams("inference is not configured")
	}
	if err := s.deps.Infer.DeleteModel(fileName); err != nil {
		return nil, err
	}
	aprot.TriggerRefresh(ctx, modelsInfoKey)
	return s.modelsInfo()
}
