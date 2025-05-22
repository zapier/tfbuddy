package git

import (
	"fmt"
	"strings"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/rs/zerolog/log"
)

func GetLastTagRef(dir string) *plumbing.Reference {
	r, err := git.PlainOpen(dir)
	if err != nil {
		log.Fatal().Msgf("could not open git repo for module: %v", err)
	}

	tagrefs, err := r.Tags()
	if err != nil {
		log.Fatal().Msgf("could not get git tags: %v", err)
	}

	var tag *plumbing.Reference
	err = tagrefs.ForEach(func(t *plumbing.Reference) error {
		log.Trace().Msg(FormatRef(t))
		tag = t
		return nil
	})
	if err != nil {
		log.Fatal().Msgf("could not iterate git tags: %v", err)
	}

	return tag
}

func GetLastTag(dir string) string {
	return CleanTagReference(GetLastTagRef(dir))
}

func CleanTagReference(tagRef *plumbing.Reference) string {
	return CleanTagRefName(tagRef.Strings()[0])
}

func CleanTagRefName(refName string) string {
	output := strings.Replace(refName, "refs/tags/", "", 1)
	return output
}

func FormatRef(ref *plumbing.Reference) string {
	return fmt.Sprintf("%s %s %s %s", ref.Name(), ref.Hash(), ref.Target(), ref.Type())
}
