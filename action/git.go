package action

import (
	"fmt"

	"github.com/fatih/color"
	"github.com/pkg/errors"
	"github.com/urfave/cli"
)

// Git runs git commands inside the store or mounts
func (s *Action) Git(c *cli.Context) error {
	store := c.String("store")
	recurse := true
	if c.IsSet("no-recurse") {
		recurse = !c.Bool("no-recurse")
	}
	force := c.Bool("force")

	if err := s.Store.Git(store, recurse, force, c.Args()...); err != nil {
		return s.exitError(ExitGit, err, "git operation failed: %s", err)
	}
	return nil
}

// GitInit initializes a git repo including basic configuration
func (s *Action) GitInit(c *cli.Context) error {
	store := c.String("store")
	sk := c.String("sign-key")

	if err := s.gitInit(store, sk); err != nil {
		return s.exitError(ExitGit, err, "failed to initialize git: %s", err)
	}
	return nil
}

func (s *Action) gitInit(store, sk string) error {
	if sk == "" {
		s, err := s.askForPrivateKey(color.CyanString("Please select a key for signing Git Commits"))
		if err == nil {
			sk = s
		}
	}

	// for convenience, set defaults to user-selected values from available private keys
	// NB: discarding returned error since this is merely a best-effort look-up for convenience
	userName, userEmail, _ := s.askForGitConfigUser()

	userName, err := s.askForString(color.CyanString("Please enter a user name for password store git config"), userName)
	if err != nil {
		return errors.Wrapf(err, "failed to ask for user input")
	}
	userEmail, err = s.askForString(color.CyanString("Please enter an email address for password store git config"), userEmail)
	if err != nil {
		return errors.Wrapf(err, "failed to ask for user input")
	}

	if err := s.Store.GitInit(store, sk, userName, userEmail); err != nil {
		return errors.Wrapf(err, "failed to run git init")
	}

	fmt.Println(color.GreenString("Git initialized"))
	return nil
}
