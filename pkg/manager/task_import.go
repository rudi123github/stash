package manager

import (
	"archive/zip"
	"context"
	"database/sql"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/stashapp/stash/pkg/database"
	"github.com/stashapp/stash/pkg/gallery"
	"github.com/stashapp/stash/pkg/image"
	"github.com/stashapp/stash/pkg/logger"
	"github.com/stashapp/stash/pkg/manager/config"
	"github.com/stashapp/stash/pkg/manager/jsonschema"
	"github.com/stashapp/stash/pkg/manager/paths"
	"github.com/stashapp/stash/pkg/models"
	"github.com/stashapp/stash/pkg/movie"
	"github.com/stashapp/stash/pkg/performer"
	"github.com/stashapp/stash/pkg/scene"
	"github.com/stashapp/stash/pkg/studio"
	"github.com/stashapp/stash/pkg/tag"
	"github.com/stashapp/stash/pkg/utils"
)

type ImportTask struct {
	txnManager models.TransactionManager
	json       jsonUtils

	BaseDir             string
	TmpZip              string
	Reset               bool
	DuplicateBehaviour  models.ImportDuplicateEnum
	MissingRefBehaviour models.ImportMissingRefEnum

	mappings            *jsonschema.Mappings
	scraped             []jsonschema.ScrapedItem
	fileNamingAlgorithm models.HashAlgorithm
}

func CreateImportTask(a models.HashAlgorithm, input models.ImportObjectsInput) (*ImportTask, error) {
	baseDir, err := instance.Paths.Generated.TempDir("import")
	if err != nil {
		logger.Errorf("error creating temporary directory for import: %s", err.Error())
		return nil, err
	}

	tmpZip := ""
	if input.File.File != nil {
		tmpZip = filepath.Join(baseDir, "import.zip")
		out, err := os.Create(tmpZip)
		if err != nil {
			return nil, err
		}

		_, err = io.Copy(out, input.File.File)
		out.Close()
		if err != nil {
			return nil, err
		}
	}

	return &ImportTask{
		txnManager:          GetInstance().TxnManager,
		BaseDir:             baseDir,
		TmpZip:              tmpZip,
		Reset:               false,
		DuplicateBehaviour:  input.DuplicateBehaviour,
		MissingRefBehaviour: input.MissingRefBehaviour,
		fileNamingAlgorithm: a,
	}, nil
}

func (t *ImportTask) GetStatus() JobStatus {
	return Import
}

func (t *ImportTask) Start(wg *sync.WaitGroup) {
	defer wg.Done()

	if t.TmpZip != "" {
		defer func() {
			err := utils.RemoveDir(t.BaseDir)
			if err != nil {
				logger.Errorf("error removing directory %s: %s", t.BaseDir, err.Error())
			}
		}()

		if err := t.unzipFile(); err != nil {
			logger.Errorf("error unzipping provided file for import: %s", err.Error())
			return
		}
	}

	t.json = jsonUtils{
		json: *paths.GetJSONPaths(t.BaseDir),
	}

	// set default behaviour if not provided
	if !t.DuplicateBehaviour.IsValid() {
		t.DuplicateBehaviour = models.ImportDuplicateEnumFail
	}
	if !t.MissingRefBehaviour.IsValid() {
		t.MissingRefBehaviour = models.ImportMissingRefEnumFail
	}

	t.mappings, _ = t.json.getMappings()
	if t.mappings == nil {
		logger.Error("missing mappings json")
		return
	}
	scraped, _ := t.json.getScraped()
	if scraped == nil {
		logger.Warn("missing scraped json")
	}
	t.scraped = scraped

	if t.Reset {
		err := database.Reset(config.GetInstance().GetDatabasePath())

		if err != nil {
			logger.Errorf("Error resetting database: %s", err.Error())
			return
		}
	}

	ctx := context.TODO()

	t.ImportTags(ctx)
	t.ImportPerformers(ctx)
	t.ImportStudios(ctx)
	t.ImportMovies(ctx)
	t.ImportGalleries(ctx)

	t.ImportScrapedItems(ctx)
	t.ImportScenes(ctx)
	t.ImportImages(ctx)
}

func (t *ImportTask) unzipFile() error {
	defer func() {
		err := os.Remove(t.TmpZip)
		if err != nil {
			logger.Errorf("error removing temporary zip file %s: %s", t.TmpZip, err.Error())
		}
	}()

	// now we can read the zip file
	r, err := zip.OpenReader(t.TmpZip)
	if err != nil {
		return err
	}
	defer r.Close()

	for _, f := range r.File {
		fn := filepath.Join(t.BaseDir, f.Name)

		if f.FileInfo().IsDir() {
			os.MkdirAll(fn, os.ModePerm)
			continue
		}

		if err := os.MkdirAll(filepath.Dir(fn), os.ModePerm); err != nil {
			return err
		}

		o, err := os.OpenFile(fn, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, f.Mode())
		if err != nil {
			return err
		}

		i, err := f.Open()
		if err != nil {
			o.Close()
			return err
		}

		if _, err := io.Copy(o, i); err != nil {
			o.Close()
			i.Close()
			return err
		}

		o.Close()
		i.Close()
	}

	return nil
}

func (t *ImportTask) ImportPerformers(ctx context.Context) {
	logger.Info("[performers] importing")

	for i, mappingJSON := range t.mappings.Performers {
		index := i + 1
		performerJSON, err := t.json.getPerformer(mappingJSON.Checksum)
		if err != nil {
			logger.Errorf("[performers] failed to read json: %s", err.Error())
			continue
		}

		logger.Progressf("[performers] %d of %d", index, len(t.mappings.Performers))

		if err := t.txnManager.WithTxn(ctx, func(r models.Repository) error {
			readerWriter := r.Performer()
			importer := &performer.Importer{
				ReaderWriter: readerWriter,
				TagWriter:    r.Tag(),
				Input:        *performerJSON,
			}

			return performImport(importer, t.DuplicateBehaviour)
		}); err != nil {
			logger.Errorf("[performers] <%s> import failed: %s", mappingJSON.Checksum, err.Error())
		}
	}

	logger.Info("[performers] import complete")
}

func (t *ImportTask) ImportStudios(ctx context.Context) {
	pendingParent := make(map[string][]*jsonschema.Studio)

	logger.Info("[studios] importing")

	for i, mappingJSON := range t.mappings.Studios {
		index := i + 1
		studioJSON, err := t.json.getStudio(mappingJSON.Checksum)
		if err != nil {
			logger.Errorf("[studios] failed to read json: %s", err.Error())
			continue
		}

		logger.Progressf("[studios] %d of %d", index, len(t.mappings.Studios))

		if err := t.txnManager.WithTxn(ctx, func(r models.Repository) error {
			return t.ImportStudio(studioJSON, pendingParent, r.Studio())
		}); err != nil {
			if err == studio.ErrParentStudioNotExist {
				// add to the pending parent list so that it is created after the parent
				s := pendingParent[studioJSON.ParentStudio]
				s = append(s, studioJSON)
				pendingParent[studioJSON.ParentStudio] = s
				continue
			}

			logger.Errorf("[studios] <%s> failed to create: %s", mappingJSON.Checksum, err.Error())
			continue
		}
	}

	// create the leftover studios, warning for missing parents
	if len(pendingParent) > 0 {
		logger.Warnf("[studios] importing studios with missing parents")

		for _, s := range pendingParent {
			for _, orphanStudioJSON := range s {
				if err := t.txnManager.WithTxn(ctx, func(r models.Repository) error {
					return t.ImportStudio(orphanStudioJSON, nil, r.Studio())
				}); err != nil {
					logger.Errorf("[studios] <%s> failed to create: %s", orphanStudioJSON.Name, err.Error())
					continue
				}
			}
		}
	}

	logger.Info("[studios] import complete")
}

func (t *ImportTask) ImportStudio(studioJSON *jsonschema.Studio, pendingParent map[string][]*jsonschema.Studio, readerWriter models.StudioReaderWriter) error {
	importer := &studio.Importer{
		ReaderWriter:        readerWriter,
		Input:               *studioJSON,
		MissingRefBehaviour: t.MissingRefBehaviour,
	}

	// first phase: return error if parent does not exist
	if pendingParent != nil {
		importer.MissingRefBehaviour = models.ImportMissingRefEnumFail
	}

	if err := performImport(importer, t.DuplicateBehaviour); err != nil {
		return err
	}

	// now create the studios pending this studios creation
	s := pendingParent[studioJSON.Name]
	for _, childStudioJSON := range s {
		// map is nil since we're not checking parent studios at this point
		if err := t.ImportStudio(childStudioJSON, nil, readerWriter); err != nil {
			return fmt.Errorf("failed to create child studio <%s>: %s", childStudioJSON.Name, err.Error())
		}
	}

	// delete the entry from the map so that we know its not left over
	delete(pendingParent, studioJSON.Name)

	return nil
}

func (t *ImportTask) ImportMovies(ctx context.Context) {
	logger.Info("[movies] importing")

	for i, mappingJSON := range t.mappings.Movies {
		index := i + 1
		movieJSON, err := t.json.getMovie(mappingJSON.Checksum)
		if err != nil {
			logger.Errorf("[movies] failed to read json: %s", err.Error())
			continue
		}

		logger.Progressf("[movies] %d of %d", index, len(t.mappings.Movies))

		if err := t.txnManager.WithTxn(ctx, func(r models.Repository) error {
			readerWriter := r.Movie()
			studioReaderWriter := r.Studio()

			movieImporter := &movie.Importer{
				ReaderWriter:        readerWriter,
				StudioWriter:        studioReaderWriter,
				Input:               *movieJSON,
				MissingRefBehaviour: t.MissingRefBehaviour,
			}

			return performImport(movieImporter, t.DuplicateBehaviour)
		}); err != nil {
			logger.Errorf("[movies] <%s> import failed: %s", mappingJSON.Checksum, err.Error())
			continue
		}
	}

	logger.Info("[movies] import complete")
}

func (t *ImportTask) ImportGalleries(ctx context.Context) {
	logger.Info("[galleries] importing")

	for i, mappingJSON := range t.mappings.Galleries {
		index := i + 1
		galleryJSON, err := t.json.getGallery(mappingJSON.Checksum)
		if err != nil {
			logger.Errorf("[galleries] failed to read json: %s", err.Error())
			continue
		}

		logger.Progressf("[galleries] %d of %d", index, len(t.mappings.Galleries))

		if err := t.txnManager.WithTxn(ctx, func(r models.Repository) error {
			readerWriter := r.Gallery()
			tagWriter := r.Tag()
			performerWriter := r.Performer()
			studioWriter := r.Studio()

			galleryImporter := &gallery.Importer{
				ReaderWriter:        readerWriter,
				PerformerWriter:     performerWriter,
				StudioWriter:        studioWriter,
				TagWriter:           tagWriter,
				Input:               *galleryJSON,
				MissingRefBehaviour: t.MissingRefBehaviour,
			}

			return performImport(galleryImporter, t.DuplicateBehaviour)
		}); err != nil {
			logger.Errorf("[galleries] <%s> import failed to commit: %s", mappingJSON.Checksum, err.Error())
			continue
		}
	}

	logger.Info("[galleries] import complete")
}

func (t *ImportTask) ImportTags(ctx context.Context) {
	logger.Info("[tags] importing")

	for i, mappingJSON := range t.mappings.Tags {
		index := i + 1
		tagJSON, err := t.json.getTag(mappingJSON.Checksum)
		if err != nil {
			logger.Errorf("[tags] failed to read json: %s", err.Error())
			continue
		}

		logger.Progressf("[tags] %d of %d", index, len(t.mappings.Tags))

		if err := t.txnManager.WithTxn(ctx, func(r models.Repository) error {
			readerWriter := r.Tag()

			tagImporter := &tag.Importer{
				ReaderWriter: readerWriter,
				Input:        *tagJSON,
			}

			return performImport(tagImporter, t.DuplicateBehaviour)
		}); err != nil {
			logger.Errorf("[tags] <%s> failed to import: %s", mappingJSON.Checksum, err.Error())
			continue
		}
	}

	logger.Info("[tags] import complete")
}

func (t *ImportTask) ImportScrapedItems(ctx context.Context) {
	if err := t.txnManager.WithTxn(ctx, func(r models.Repository) error {
		logger.Info("[scraped sites] importing")
		qb := r.ScrapedItem()
		sqb := r.Studio()
		currentTime := time.Now()

		for i, mappingJSON := range t.scraped {
			index := i + 1
			logger.Progressf("[scraped sites] %d of %d", index, len(t.mappings.Scenes))

			newScrapedItem := models.ScrapedItem{
				Title:           sql.NullString{String: mappingJSON.Title, Valid: true},
				Description:     sql.NullString{String: mappingJSON.Description, Valid: true},
				URL:             sql.NullString{String: mappingJSON.URL, Valid: true},
				Date:            models.SQLiteDate{String: mappingJSON.Date, Valid: true},
				Rating:          sql.NullString{String: mappingJSON.Rating, Valid: true},
				Tags:            sql.NullString{String: mappingJSON.Tags, Valid: true},
				Models:          sql.NullString{String: mappingJSON.Models, Valid: true},
				Episode:         sql.NullInt64{Int64: int64(mappingJSON.Episode), Valid: true},
				GalleryFilename: sql.NullString{String: mappingJSON.GalleryFilename, Valid: true},
				GalleryURL:      sql.NullString{String: mappingJSON.GalleryURL, Valid: true},
				VideoFilename:   sql.NullString{String: mappingJSON.VideoFilename, Valid: true},
				VideoURL:        sql.NullString{String: mappingJSON.VideoURL, Valid: true},
				CreatedAt:       models.SQLiteTimestamp{Timestamp: currentTime},
				UpdatedAt:       models.SQLiteTimestamp{Timestamp: t.getTimeFromJSONTime(mappingJSON.UpdatedAt)},
			}

			studio, err := sqb.FindByName(mappingJSON.Studio, false)
			if err != nil {
				logger.Errorf("[scraped sites] failed to fetch studio: %s", err.Error())
			}
			if studio != nil {
				newScrapedItem.StudioID = sql.NullInt64{Int64: int64(studio.ID), Valid: true}
			}

			_, err = qb.Create(newScrapedItem)
			if err != nil {
				logger.Errorf("[scraped sites] <%s> failed to create: %s", newScrapedItem.Title.String, err.Error())
			}
		}

		return nil
	}); err != nil {
		logger.Errorf("[scraped sites] import failed to commit: %s", err.Error())
	}

	logger.Info("[scraped sites] import complete")
}

func (t *ImportTask) ImportScenes(ctx context.Context) {
	logger.Info("[scenes] importing")

	for i, mappingJSON := range t.mappings.Scenes {
		index := i + 1

		logger.Progressf("[scenes] %d of %d", index, len(t.mappings.Scenes))

		sceneJSON, err := t.json.getScene(mappingJSON.Checksum)
		if err != nil {
			logger.Infof("[scenes] <%s> json parse failure: %s", mappingJSON.Checksum, err.Error())
			continue
		}

		sceneHash := mappingJSON.Checksum

		if err := t.txnManager.WithTxn(ctx, func(r models.Repository) error {
			readerWriter := r.Scene()
			tagWriter := r.Tag()
			galleryWriter := r.Gallery()
			movieWriter := r.Movie()
			performerWriter := r.Performer()
			studioWriter := r.Studio()
			markerWriter := r.SceneMarker()

			sceneImporter := &scene.Importer{
				ReaderWriter: readerWriter,
				Input:        *sceneJSON,
				Path:         mappingJSON.Path,

				FileNamingAlgorithm: t.fileNamingAlgorithm,
				MissingRefBehaviour: t.MissingRefBehaviour,

				GalleryWriter:   galleryWriter,
				MovieWriter:     movieWriter,
				PerformerWriter: performerWriter,
				StudioWriter:    studioWriter,
				TagWriter:       tagWriter,
			}

			if err := performImport(sceneImporter, t.DuplicateBehaviour); err != nil {
				return err
			}

			// import the scene markers
			for _, m := range sceneJSON.Markers {
				markerImporter := &scene.MarkerImporter{
					SceneID:             sceneImporter.ID,
					Input:               m,
					MissingRefBehaviour: t.MissingRefBehaviour,
					ReaderWriter:        markerWriter,
					TagWriter:           tagWriter,
				}

				if err := performImport(markerImporter, t.DuplicateBehaviour); err != nil {
					return err
				}
			}

			return nil
		}); err != nil {
			logger.Errorf("[scenes] <%s> import failed: %s", sceneHash, err.Error())
		}
	}

	logger.Info("[scenes] import complete")
}

func (t *ImportTask) ImportImages(ctx context.Context) {
	logger.Info("[images] importing")

	for i, mappingJSON := range t.mappings.Images {
		index := i + 1

		logger.Progressf("[images] %d of %d", index, len(t.mappings.Images))

		imageJSON, err := t.json.getImage(mappingJSON.Checksum)
		if err != nil {
			logger.Infof("[images] <%s> json parse failure: %s", mappingJSON.Checksum, err.Error())
			continue
		}

		imageHash := mappingJSON.Checksum

		if err := t.txnManager.WithTxn(ctx, func(r models.Repository) error {
			readerWriter := r.Image()
			tagWriter := r.Tag()
			galleryWriter := r.Gallery()
			performerWriter := r.Performer()
			studioWriter := r.Studio()

			imageImporter := &image.Importer{
				ReaderWriter: readerWriter,
				Input:        *imageJSON,
				Path:         mappingJSON.Path,

				MissingRefBehaviour: t.MissingRefBehaviour,

				GalleryWriter:   galleryWriter,
				PerformerWriter: performerWriter,
				StudioWriter:    studioWriter,
				TagWriter:       tagWriter,
			}

			return performImport(imageImporter, t.DuplicateBehaviour)
		}); err != nil {
			logger.Errorf("[images] <%s> import failed: %s", imageHash, err.Error())
		}
	}

	logger.Info("[images] import complete")
}

func (t *ImportTask) getPerformers(names []string, qb models.PerformerReader) ([]*models.Performer, error) {
	performers, err := qb.FindByNames(names, false)
	if err != nil {
		return nil, err
	}

	var pluckedNames []string
	for _, performer := range performers {
		if !performer.Name.Valid {
			continue
		}
		pluckedNames = append(pluckedNames, performer.Name.String)
	}

	missingPerformers := utils.StrFilter(names, func(name string) bool {
		return !utils.StrInclude(pluckedNames, name)
	})

	for _, missingPerformer := range missingPerformers {
		logger.Warnf("[scenes] performer %s does not exist", missingPerformer)
	}

	return performers, nil
}

func (t *ImportTask) getMoviesScenes(input []jsonschema.SceneMovie, sceneID int, mqb models.MovieReader) ([]models.MoviesScenes, error) {
	var movies []models.MoviesScenes
	for _, inputMovie := range input {
		movie, err := mqb.FindByName(inputMovie.MovieName, false)
		if err != nil {
			return nil, err
		}

		if movie == nil {
			logger.Warnf("[scenes] movie %s does not exist", inputMovie.MovieName)
		} else {
			toAdd := models.MoviesScenes{
				MovieID: movie.ID,
				SceneID: sceneID,
			}

			if inputMovie.SceneIndex != 0 {
				toAdd.SceneIndex = sql.NullInt64{
					Int64: int64(inputMovie.SceneIndex),
					Valid: true,
				}
			}

			movies = append(movies, toAdd)
		}
	}

	return movies, nil
}

func (t *ImportTask) getTags(sceneChecksum string, names []string, tqb models.TagReader) ([]*models.Tag, error) {
	tags, err := tqb.FindByNames(names, false)
	if err != nil {
		return nil, err
	}

	var pluckedNames []string
	for _, tag := range tags {
		if tag.Name == "" {
			continue
		}
		pluckedNames = append(pluckedNames, tag.Name)
	}

	missingTags := utils.StrFilter(names, func(name string) bool {
		return !utils.StrInclude(pluckedNames, name)
	})

	for _, missingTag := range missingTags {
		logger.Warnf("[scenes] <%s> tag %s does not exist", sceneChecksum, missingTag)
	}

	return tags, nil
}

// https://www.reddit.com/r/golang/comments/5ia523/idiomatic_way_to_remove_duplicates_in_a_slice/db6qa2e
func (t *ImportTask) getUnique(s []string) []string {
	seen := make(map[string]struct{}, len(s))
	j := 0
	for _, v := range s {
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		s[j] = v
		j++
	}
	return s[:j]
}

var currentLocation = time.Now().Location()

func (t *ImportTask) getTimeFromJSONTime(jsonTime models.JSONTime) time.Time {
	if currentLocation != nil {
		if jsonTime.IsZero() {
			return time.Now().In(currentLocation)
		} else {
			return jsonTime.Time.In(currentLocation)
		}
	} else {
		if jsonTime.IsZero() {
			return time.Now()
		} else {
			return jsonTime.Time
		}
	}
}
