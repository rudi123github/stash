// +build integration

package sqlite_test

import (
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/stashapp/stash/pkg/models"
	"github.com/stashapp/stash/pkg/utils"
)

func TestPerformerFindBySceneID(t *testing.T) {
	withTxn(func(r models.Repository) error {
		pqb := r.Performer()
		sceneID := sceneIDs[sceneIdxWithPerformer]

		performers, err := pqb.FindBySceneID(sceneID)

		if err != nil {
			t.Errorf("Error finding performer: %s", err.Error())
		}

		assert.Equal(t, 1, len(performers))
		performer := performers[0]

		assert.Equal(t, getPerformerStringValue(performerIdxWithScene, "Name"), performer.Name.String)

		performers, err = pqb.FindBySceneID(0)

		if err != nil {
			t.Errorf("Error finding performer: %s", err.Error())
		}

		assert.Equal(t, 0, len(performers))

		return nil
	})
}

func TestPerformerFindByNames(t *testing.T) {
	getNames := func(p []*models.Performer) []string {
		var ret []string
		for _, pp := range p {
			ret = append(ret, pp.Name.String)
		}
		return ret
	}

	withTxn(func(r models.Repository) error {
		var names []string

		pqb := r.Performer()

		names = append(names, performerNames[performerIdxWithScene]) // find performers by names

		performers, err := pqb.FindByNames(names, false)
		if err != nil {
			t.Errorf("Error finding performers: %s", err.Error())
		}
		assert.Len(t, performers, 1)
		assert.Equal(t, performerNames[performerIdxWithScene], performers[0].Name.String)

		performers, err = pqb.FindByNames(names, true) // find performers by names nocase
		if err != nil {
			t.Errorf("Error finding performers: %s", err.Error())
		}
		assert.Len(t, performers, 2) // performerIdxWithScene and performerIdxWithDupName
		assert.Equal(t, strings.ToLower(performerNames[performerIdxWithScene]), strings.ToLower(performers[0].Name.String))
		assert.Equal(t, strings.ToLower(performerNames[performerIdxWithScene]), strings.ToLower(performers[1].Name.String))

		names = append(names, performerNames[performerIdx1WithScene]) // find performers by names ( 2 names )

		performers, err = pqb.FindByNames(names, false)
		if err != nil {
			t.Errorf("Error finding performers: %s", err.Error())
		}
		retNames := getNames(performers)
		assert.Equal(t, names, retNames)

		performers, err = pqb.FindByNames(names, true) // find performers by names ( 2 names nocase)
		if err != nil {
			t.Errorf("Error finding performers: %s", err.Error())
		}
		retNames = getNames(performers)
		assert.Equal(t, []string{
			performerNames[performerIdxWithScene],
			performerNames[performerIdx1WithScene],
			performerNames[performerIdx1WithDupName],
			performerNames[performerIdxWithDupName],
		}, retNames)

		return nil
	})
}

func TestPerformerQueryForAutoTag(t *testing.T) {
	withTxn(func(r models.Repository) error {
		tqb := r.Performer()

		name := performerNames[performerIdxWithScene] // find a performer by name

		performers, err := tqb.QueryForAutoTag([]string{name})

		if err != nil {
			t.Errorf("Error finding performers: %s", err.Error())
		}

		assert.Len(t, performers, 2)
		assert.Equal(t, strings.ToLower(performerNames[performerIdxWithScene]), strings.ToLower(performers[0].Name.String))
		assert.Equal(t, strings.ToLower(performerNames[performerIdxWithScene]), strings.ToLower(performers[1].Name.String))

		return nil
	})
}

func TestPerformerUpdatePerformerImage(t *testing.T) {
	if err := withTxn(func(r models.Repository) error {
		qb := r.Performer()

		// create performer to test against
		const name = "TestPerformerUpdatePerformerImage"
		performer := models.Performer{
			Name:     sql.NullString{String: name, Valid: true},
			Checksum: utils.MD5FromString(name),
			Favorite: sql.NullBool{Bool: false, Valid: true},
		}
		created, err := qb.Create(performer)
		if err != nil {
			return fmt.Errorf("Error creating performer: %s", err.Error())
		}

		image := []byte("image")
		err = qb.UpdateImage(created.ID, image)
		if err != nil {
			return fmt.Errorf("Error updating performer image: %s", err.Error())
		}

		// ensure image set
		storedImage, err := qb.GetImage(created.ID)
		if err != nil {
			return fmt.Errorf("Error getting image: %s", err.Error())
		}
		assert.Equal(t, storedImage, image)

		// set nil image
		err = qb.UpdateImage(created.ID, nil)
		if err == nil {
			return fmt.Errorf("Expected error setting nil image")
		}

		return nil
	}); err != nil {
		t.Error(err.Error())
	}
}

func TestPerformerDestroyPerformerImage(t *testing.T) {
	if err := withTxn(func(r models.Repository) error {
		qb := r.Performer()

		// create performer to test against
		const name = "TestPerformerDestroyPerformerImage"
		performer := models.Performer{
			Name:     sql.NullString{String: name, Valid: true},
			Checksum: utils.MD5FromString(name),
			Favorite: sql.NullBool{Bool: false, Valid: true},
		}
		created, err := qb.Create(performer)
		if err != nil {
			return fmt.Errorf("Error creating performer: %s", err.Error())
		}

		image := []byte("image")
		err = qb.UpdateImage(created.ID, image)
		if err != nil {
			return fmt.Errorf("Error updating performer image: %s", err.Error())
		}

		err = qb.DestroyImage(created.ID)
		if err != nil {
			return fmt.Errorf("Error destroying performer image: %s", err.Error())
		}

		// image should be nil
		storedImage, err := qb.GetImage(created.ID)
		if err != nil {
			return fmt.Errorf("Error getting image: %s", err.Error())
		}
		assert.Nil(t, storedImage)

		return nil
	}); err != nil {
		t.Error(err.Error())
	}
}

func TestPerformerQueryAge(t *testing.T) {
	const age = 19
	ageCriterion := models.IntCriterionInput{
		Value:    age,
		Modifier: models.CriterionModifierEquals,
	}

	verifyPerformerAge(t, ageCriterion)

	ageCriterion.Modifier = models.CriterionModifierNotEquals
	verifyPerformerAge(t, ageCriterion)

	ageCriterion.Modifier = models.CriterionModifierGreaterThan
	verifyPerformerAge(t, ageCriterion)

	ageCriterion.Modifier = models.CriterionModifierLessThan
	verifyPerformerAge(t, ageCriterion)
}

func verifyPerformerAge(t *testing.T, ageCriterion models.IntCriterionInput) {
	withTxn(func(r models.Repository) error {
		qb := r.Performer()
		performerFilter := models.PerformerFilterType{
			Age: &ageCriterion,
		}

		performers, _, err := qb.Query(&performerFilter, nil)
		if err != nil {
			t.Errorf("Error querying performer: %s", err.Error())
		}

		now := time.Now()
		for _, performer := range performers {
			cd := now

			if performer.DeathDate.Valid {
				cd, _ = time.Parse("2006-01-02", performer.DeathDate.String)
			}

			bd := performer.Birthdate.String
			d, _ := time.Parse("2006-01-02", bd)
			age := cd.Year() - d.Year()
			if cd.YearDay() < d.YearDay() {
				age = age - 1
			}

			verifyInt(t, age, ageCriterion)
		}

		return nil
	})
}

func TestPerformerQueryCareerLength(t *testing.T) {
	const value = "2005"
	careerLengthCriterion := models.StringCriterionInput{
		Value:    value,
		Modifier: models.CriterionModifierEquals,
	}

	verifyPerformerCareerLength(t, careerLengthCriterion)

	careerLengthCriterion.Modifier = models.CriterionModifierNotEquals
	verifyPerformerCareerLength(t, careerLengthCriterion)

	careerLengthCriterion.Modifier = models.CriterionModifierMatchesRegex
	verifyPerformerCareerLength(t, careerLengthCriterion)

	careerLengthCriterion.Modifier = models.CriterionModifierNotMatchesRegex
	verifyPerformerCareerLength(t, careerLengthCriterion)
}

func verifyPerformerCareerLength(t *testing.T, criterion models.StringCriterionInput) {
	withTxn(func(r models.Repository) error {
		qb := r.Performer()
		performerFilter := models.PerformerFilterType{
			CareerLength: &criterion,
		}

		performers, _, err := qb.Query(&performerFilter, nil)
		if err != nil {
			t.Errorf("Error querying performer: %s", err.Error())
		}

		for _, performer := range performers {
			cl := performer.CareerLength
			verifyNullString(t, cl, criterion)
		}

		return nil
	})
}

func TestPerformerQueryURL(t *testing.T) {
	const sceneIdx = 1
	performerURL := getPerformerStringValue(sceneIdx, urlField)

	urlCriterion := models.StringCriterionInput{
		Value:    performerURL,
		Modifier: models.CriterionModifierEquals,
	}

	filter := models.PerformerFilterType{
		URL: &urlCriterion,
	}

	verifyFn := func(g *models.Performer) {
		t.Helper()
		verifyNullString(t, g.URL, urlCriterion)
	}

	verifyPerformerQuery(t, filter, verifyFn)

	urlCriterion.Modifier = models.CriterionModifierNotEquals
	verifyPerformerQuery(t, filter, verifyFn)

	urlCriterion.Modifier = models.CriterionModifierMatchesRegex
	urlCriterion.Value = "performer_.*1_URL"
	verifyPerformerQuery(t, filter, verifyFn)

	urlCriterion.Modifier = models.CriterionModifierNotMatchesRegex
	verifyPerformerQuery(t, filter, verifyFn)

	urlCriterion.Modifier = models.CriterionModifierIsNull
	urlCriterion.Value = ""
	verifyPerformerQuery(t, filter, verifyFn)

	urlCriterion.Modifier = models.CriterionModifierNotNull
	verifyPerformerQuery(t, filter, verifyFn)
}

func verifyPerformerQuery(t *testing.T, filter models.PerformerFilterType, verifyFn func(s *models.Performer)) {
	withTxn(func(r models.Repository) error {
		t.Helper()
		sqb := r.Performer()

		performers := queryPerformers(t, sqb, &filter, nil)

		// assume it should find at least one
		assert.Greater(t, len(performers), 0)

		for _, p := range performers {
			verifyFn(p)
		}

		return nil
	})
}

func queryPerformers(t *testing.T, qb models.PerformerReader, performerFilter *models.PerformerFilterType, findFilter *models.FindFilterType) []*models.Performer {
	performers, _, err := qb.Query(performerFilter, findFilter)
	if err != nil {
		t.Errorf("Error querying performers: %s", err.Error())
	}

	return performers
}

func TestPerformerQueryTags(t *testing.T) {
	withTxn(func(r models.Repository) error {
		sqb := r.Performer()
		tagCriterion := models.MultiCriterionInput{
			Value: []string{
				strconv.Itoa(tagIDs[tagIdxWithPerformer]),
				strconv.Itoa(tagIDs[tagIdx1WithPerformer]),
			},
			Modifier: models.CriterionModifierIncludes,
		}

		performerFilter := models.PerformerFilterType{
			Tags: &tagCriterion,
		}

		// ensure ids are correct
		performers := queryPerformers(t, sqb, &performerFilter, nil)
		assert.Len(t, performers, 2)
		for _, performer := range performers {
			assert.True(t, performer.ID == performerIDs[performerIdxWithTag] || performer.ID == performerIDs[performerIdxWithTwoTags])
		}

		tagCriterion = models.MultiCriterionInput{
			Value: []string{
				strconv.Itoa(tagIDs[tagIdx1WithPerformer]),
				strconv.Itoa(tagIDs[tagIdx2WithPerformer]),
			},
			Modifier: models.CriterionModifierIncludesAll,
		}

		performers = queryPerformers(t, sqb, &performerFilter, nil)

		assert.Len(t, performers, 1)
		assert.Equal(t, sceneIDs[performerIdxWithTwoTags], performers[0].ID)

		tagCriterion = models.MultiCriterionInput{
			Value: []string{
				strconv.Itoa(tagIDs[tagIdx1WithPerformer]),
			},
			Modifier: models.CriterionModifierExcludes,
		}

		q := getSceneStringValue(performerIdxWithTwoTags, titleField)
		findFilter := models.FindFilterType{
			Q: &q,
		}

		performers = queryPerformers(t, sqb, &performerFilter, &findFilter)
		assert.Len(t, performers, 0)

		return nil
	})
}

func TestPerformerQueryTagCount(t *testing.T) {
	const tagCount = 1
	tagCountCriterion := models.IntCriterionInput{
		Value:    tagCount,
		Modifier: models.CriterionModifierEquals,
	}

	verifyPerformersTagCount(t, tagCountCriterion)

	tagCountCriterion.Modifier = models.CriterionModifierNotEquals
	verifyPerformersTagCount(t, tagCountCriterion)

	tagCountCriterion.Modifier = models.CriterionModifierGreaterThan
	verifyPerformersTagCount(t, tagCountCriterion)

	tagCountCriterion.Modifier = models.CriterionModifierLessThan
	verifyPerformersTagCount(t, tagCountCriterion)
}

func verifyPerformersTagCount(t *testing.T, tagCountCriterion models.IntCriterionInput) {
	withTxn(func(r models.Repository) error {
		sqb := r.Performer()
		performerFilter := models.PerformerFilterType{
			TagCount: &tagCountCriterion,
		}

		performers := queryPerformers(t, sqb, &performerFilter, nil)
		assert.Greater(t, len(performers), 0)

		for _, performer := range performers {
			ids, err := sqb.GetTagIDs(performer.ID)
			if err != nil {
				return err
			}
			verifyInt(t, len(ids), tagCountCriterion)
		}

		return nil
	})
}

func TestPerformerQuerySceneCount(t *testing.T) {
	const sceneCount = 1
	sceneCountCriterion := models.IntCriterionInput{
		Value:    sceneCount,
		Modifier: models.CriterionModifierEquals,
	}

	verifyPerformersSceneCount(t, sceneCountCriterion)

	sceneCountCriterion.Modifier = models.CriterionModifierNotEquals
	verifyPerformersSceneCount(t, sceneCountCriterion)

	sceneCountCriterion.Modifier = models.CriterionModifierGreaterThan
	verifyPerformersSceneCount(t, sceneCountCriterion)

	sceneCountCriterion.Modifier = models.CriterionModifierLessThan
	verifyPerformersSceneCount(t, sceneCountCriterion)
}

func verifyPerformersSceneCount(t *testing.T, sceneCountCriterion models.IntCriterionInput) {
	withTxn(func(r models.Repository) error {
		sqb := r.Performer()
		performerFilter := models.PerformerFilterType{
			SceneCount: &sceneCountCriterion,
		}

		performers := queryPerformers(t, sqb, &performerFilter, nil)
		assert.Greater(t, len(performers), 0)

		for _, performer := range performers {
			ids, err := r.Scene().FindByPerformerID(performer.ID)
			if err != nil {
				return err
			}
			verifyInt(t, len(ids), sceneCountCriterion)
		}

		return nil
	})
}

func TestPerformerQueryImageCount(t *testing.T) {
	const imageCount = 1
	imageCountCriterion := models.IntCriterionInput{
		Value:    imageCount,
		Modifier: models.CriterionModifierEquals,
	}

	verifyPerformersImageCount(t, imageCountCriterion)

	imageCountCriterion.Modifier = models.CriterionModifierNotEquals
	verifyPerformersImageCount(t, imageCountCriterion)

	imageCountCriterion.Modifier = models.CriterionModifierGreaterThan
	verifyPerformersImageCount(t, imageCountCriterion)

	imageCountCriterion.Modifier = models.CriterionModifierLessThan
	verifyPerformersImageCount(t, imageCountCriterion)
}

func verifyPerformersImageCount(t *testing.T, imageCountCriterion models.IntCriterionInput) {
	withTxn(func(r models.Repository) error {
		sqb := r.Performer()
		performerFilter := models.PerformerFilterType{
			ImageCount: &imageCountCriterion,
		}

		performers := queryPerformers(t, sqb, &performerFilter, nil)
		assert.Greater(t, len(performers), 0)

		for _, performer := range performers {
			pp := 0

			_, count, err := r.Image().Query(&models.ImageFilterType{
				Performers: &models.MultiCriterionInput{
					Value:    []string{strconv.Itoa(performer.ID)},
					Modifier: models.CriterionModifierIncludes,
				},
			}, &models.FindFilterType{
				PerPage: &pp,
			})
			if err != nil {
				return err
			}
			verifyInt(t, count, imageCountCriterion)
		}

		return nil
	})
}

func TestPerformerQueryGalleryCount(t *testing.T) {
	const galleryCount = 1
	galleryCountCriterion := models.IntCriterionInput{
		Value:    galleryCount,
		Modifier: models.CriterionModifierEquals,
	}

	verifyPerformersGalleryCount(t, galleryCountCriterion)

	galleryCountCriterion.Modifier = models.CriterionModifierNotEquals
	verifyPerformersGalleryCount(t, galleryCountCriterion)

	galleryCountCriterion.Modifier = models.CriterionModifierGreaterThan
	verifyPerformersGalleryCount(t, galleryCountCriterion)

	galleryCountCriterion.Modifier = models.CriterionModifierLessThan
	verifyPerformersGalleryCount(t, galleryCountCriterion)
}

func verifyPerformersGalleryCount(t *testing.T, galleryCountCriterion models.IntCriterionInput) {
	withTxn(func(r models.Repository) error {
		sqb := r.Performer()
		performerFilter := models.PerformerFilterType{
			GalleryCount: &galleryCountCriterion,
		}

		performers := queryPerformers(t, sqb, &performerFilter, nil)
		assert.Greater(t, len(performers), 0)

		for _, performer := range performers {
			pp := 0

			_, count, err := r.Gallery().Query(&models.GalleryFilterType{
				Performers: &models.MultiCriterionInput{
					Value:    []string{strconv.Itoa(performer.ID)},
					Modifier: models.CriterionModifierIncludes,
				},
			}, &models.FindFilterType{
				PerPage: &pp,
			})
			if err != nil {
				return err
			}
			verifyInt(t, count, galleryCountCriterion)
		}

		return nil
	})
}

func TestPerformerStashIDs(t *testing.T) {
	if err := withTxn(func(r models.Repository) error {
		qb := r.Performer()

		// create performer to test against
		const name = "TestStashIDs"
		performer := models.Performer{
			Name:     sql.NullString{String: name, Valid: true},
			Checksum: utils.MD5FromString(name),
			Favorite: sql.NullBool{Bool: false, Valid: true},
		}
		created, err := qb.Create(performer)
		if err != nil {
			return fmt.Errorf("Error creating performer: %s", err.Error())
		}

		testStashIDReaderWriter(t, qb, created.ID)
		return nil
	}); err != nil {
		t.Error(err.Error())
	}
}
func TestPerformerQueryRating(t *testing.T) {
	const rating = 3
	ratingCriterion := models.IntCriterionInput{
		Value:    rating,
		Modifier: models.CriterionModifierEquals,
	}

	verifyPerformersRating(t, ratingCriterion)

	ratingCriterion.Modifier = models.CriterionModifierNotEquals
	verifyPerformersRating(t, ratingCriterion)

	ratingCriterion.Modifier = models.CriterionModifierGreaterThan
	verifyPerformersRating(t, ratingCriterion)

	ratingCriterion.Modifier = models.CriterionModifierLessThan
	verifyPerformersRating(t, ratingCriterion)

	ratingCriterion.Modifier = models.CriterionModifierIsNull
	verifyPerformersRating(t, ratingCriterion)

	ratingCriterion.Modifier = models.CriterionModifierNotNull
	verifyPerformersRating(t, ratingCriterion)
}

func verifyPerformersRating(t *testing.T, ratingCriterion models.IntCriterionInput) {
	withTxn(func(r models.Repository) error {
		sqb := r.Performer()
		performerFilter := models.PerformerFilterType{
			Rating: &ratingCriterion,
		}

		performers := queryPerformers(t, sqb, &performerFilter, nil)

		for _, performer := range performers {
			verifyInt64(t, performer.Rating, ratingCriterion)
		}

		return nil
	})
}

func TestPerformerQueryIsMissingRating(t *testing.T) {
	withTxn(func(r models.Repository) error {
		sqb := r.Performer()
		isMissing := "rating"
		performerFilter := models.PerformerFilterType{
			IsMissing: &isMissing,
		}

		performers := queryPerformers(t, sqb, &performerFilter, nil)

		assert.True(t, len(performers) > 0)

		for _, performer := range performers {
			assert.True(t, !performer.Rating.Valid)
		}

		return nil
	})
}

// TODO Update
// TODO Destroy
// TODO Find
// TODO Count
// TODO All
// TODO AllSlim
// TODO Query
