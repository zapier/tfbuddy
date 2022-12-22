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
	tagrefs.ForEach(func(t *plumbing.Reference) error {
		log.Trace().Msg(FormatRef(t))
		tag = t
		return nil
	})

	return tag
}

func GetLastTag(dir string) string {
	return CleanTagReference(GetLastTagRef(dir))
}

// clean the head ref
func cleanHeadReference(tagRef *plumbing.Reference) string {
	return cleanHeadRefName(tagRef.Strings()[0])
}

func cleanHeadRefName(refName string) string {
	output := strings.Replace(refName, "refs/tags/", "", 1)
	return output
}

// clean the tag ref
func CleanTagReference(tagRef *plumbing.Reference) string {
	return CleanTagRefName(tagRef.Strings()[0])
}

func CleanTagRefName(refName string) string {
	output := strings.Replace(refName, "refs/tags/", "", 1)
	return output
}

func CheckoutHead(dir string, branch string) error {
	r, _, err := openRepo(dir)
	if err != nil {
		return err
	}

	log.Debug().Msg("checking out HEAD")
	ref, err := r.Head()
	if err != nil {
		return err
	}

	return CheckoutRef(dir, ref)
}

func CheckoutTag(dir string, tag string) error {
	r, _, err := openRepo(dir)
	if err != nil {
		return err
	}

	log.Debug().Msgf("checking out tag: %s", tag)
	ref, err := r.Tag(tag)
	if err != nil {
		return err
	}
	return CheckoutRef(dir, ref)
}

func CheckoutRef(dir string, ref *plumbing.Reference) error {
	_, w, err := openRepo(dir)
	if err != nil {
		return err
	}

	status, err := w.Status()
	if err != nil {
		return err
	}
	if !status.IsClean() {
		log.Fatal().Msgf("module directory has uncommited changes, cannot checkout ref: %s", ref)
	}

	log.Debug().Msgf("checking out: %s", FormatRef(ref))

	err = w.Checkout(&git.CheckoutOptions{
		Hash: ref.Hash(),
	})
	if err != nil {
		return err
	}

	return nil
}

func CheckoutRefName(dir string, ref *plumbing.Reference) error {
	_, w, err := openRepo(dir)
	if err != nil {
		return err
	}

	status, err := w.Status()
	if err != nil {
		return err
	}
	if !status.IsClean() {
		log.Fatal().Msgf("module directory has uncommited changes, cannot checkout ref: %s", ref)
	}

	log.Debug().Msgf("checking out: %s", FormatRef(ref))

	err = w.Checkout(&git.CheckoutOptions{
		Branch: ref.Name(),
	})
	if err != nil {
		return err
	}

	return nil
}

func GetHeadCommit(dir string) (ref *plumbing.Reference, err error) {
	r, _, err := openRepo(dir)
	if err != nil {
		return nil, err
	}

	return r.Head()
}

func openRepo(dir string) (*git.Repository, *git.Worktree, error) {
	r, err := git.PlainOpen(dir)
	if err != nil {
		return nil, nil, err
	}
	w, err := r.Worktree()
	if err != nil {
		return nil, nil, err
	}

	return r, w, nil
}

func FormatRef(ref *plumbing.Reference) string {
	return fmt.Sprintf("%s %s %s %s", ref.Name(), ref.Hash(), ref.Target(), ref.Type())
}
