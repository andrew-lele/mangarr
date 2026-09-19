package templater

import (
	"testing"

	"mangarr/internal/domain"

	"github.com/stretchr/testify/assert"
)

func TestExecTemplateUsesCanonicalChapterNumber(t *testing.T) {
	tpl := New(
		domain.Manga{Title: "Series"},
		domain.Chapter{Number: mustChapterNumber("1.50"), Title: "Pilot"},
	)

	assert.Equal(t, "Series Ch. 1.5 - Pilot", tpl.ExecTemplate("{manga:<.>} Ch. {num}{title: - <.>}"))
}

func TestExecTemplatePadsWholePart(t *testing.T) {
	tpl := New(
		domain.Manga{Title: "Series"},
		domain.Chapter{Number: mustChapterNumber("10.01")},
	)

	assert.Equal(t, "0010.01", tpl.ExecTemplate("{num:4}"))
}

func TestExecTemplateDropsRedundantNumberEchoTitle(t *testing.T) {
	// *arr-style: a title that merely echoes the chapter number is dropped
	// so filenames are not redundant (Ch. 131 -> "131", not "131 - Chapter 131").
	type numTitlePair struct {
		Num   string
		Title string
	}
	cases := []numTitlePair{
		{Num: "131", Title: "Chapter 131"},
		{Num: "131", Title: "Ch. 131"},
		{Num: "140", Title: "Mission 140"},
		{Num: "131", Title: "131"},
	}
	for _, c := range cases {
		tpl := New(
			domain.Manga{Title: "Series"},
			domain.Chapter{Number: mustChapterNumber(c.Num), Title: c.Title},
		)
		assert.Equal(t, c.Num, tpl.ExecTemplate("{num}{title: - <.>}"))
	}

	// real titles are kept with the dash suffix
	tpl := New(
		domain.Manga{Title: "Series"},
		domain.Chapter{Number: mustChapterNumber("131"), Title: "The Showdown"},
	)
	assert.Equal(t, "131 - The Showdown", tpl.ExecTemplate("{num}{title: - <.>}"))
}

func TestExecTemplateKeepsRealTitleWithSuffix(t *testing.T) {
	tpl := New(
		domain.Manga{Title: "Series"},
		domain.Chapter{Number: mustChapterNumber("1.50"), Title: "Pilot"},
	)

	assert.Equal(t, "1.5 - Pilot", tpl.ExecTemplate("{num}{title: - <.>}"))
}

func mustChapterNumber(input string) domain.ChapterNumber {
	number, err := domain.ParseChapterNumber(input)
	if err != nil {
		panic(err)
	}
	return number
}
