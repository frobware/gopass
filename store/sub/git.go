package sub

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/blang/semver"
	"github.com/fatih/color"
	"github.com/justwatchcom/gopass/fsutil"
	"github.com/justwatchcom/gopass/store"
	"github.com/pkg/errors"
)

func (s *Store) gitCmd(name string, args ...string) error {
	cmd := exec.Command("git", args[0:]...)
	cmd.Dir = s.path
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if s.debug {
		fmt.Printf("[DEBUG] store.%s: %s %+v\n", name, cmd.Path, cmd.Args)
	}
	if err := cmd.Run(); err != nil {
		return errors.Wrapf(err, "failed to run command %s %+v", cmd.Path, cmd.Args)
	}
	// load keys only after git pull
	if len(cmd.Args) > 1 && cmd.Args[1] == "pull" {
		if s.debug {
			fmt.Printf("[DEBUG] importing possilby missing keys ...\n")
		}
		if err := s.ImportMissingPublicKeys(); err != nil {
			return errors.Wrapf(err, "failed to import possibly missing public keys")
		}
	}

	return nil
}

func (s *Store) gitFixConfig() error {
	// set push default, to avoid issues with
	// "fatal: The current branch master has multiple upstream branches, refusing to push"
	// https://stackoverflow.com/questions/948354/default-behavior-of-git-push-without-a-branch-specified
	if err := s.gitCmd("GitInit", "config", "--local", "push.default", "matching"); err != nil {
		return errors.Wrapf(err, "failed to set git config for push.default")
	}

	// setup for proper diffs
	if err := s.gitCmd("GitInit", "config", "--local", "diff.gpg.binary", "true"); err != nil {
		fmt.Println(color.YellowString("Error while initializing git: %s", err))
	}
	if err := s.gitCmd("GitInit", "config", "--local", "diff.gpg.textconv", "gpg --no-tty --decrypt"); err != nil {
		fmt.Println(color.YellowString("Error while initializing git: %s", err))
	}

	return nil
}

// GitInit initializes this store's git repo and
// recursively calls GitInit on all substores.
func (s *Store) GitInit(alias, signKey, userName, userEmail string) error {
	// the git repo may be empty (i.e. no branches, cloned from a fresh remote)
	// or already initialized. Only run git init if the folder is completely empty
	if !s.isGit() {
		if err := s.gitCmd("GitInit", "init"); err != nil {
			return errors.Errorf("Failed to initialize git: %s", err)
		}
	}

	// set commit identity
	if err := s.gitCmd("GitInit", "config", "--local", "user.name", userName); err != nil {
		return errors.Wrapf(err, "failed to set git config user.name")
	}
	if err := s.gitCmd("GitInit", "config", "--local", "user.email", userEmail); err != nil {
		return errors.Wrapf(err, "failed to set git config user.email")
	}

	// ensure sane git config
	if err := s.gitFixConfig(); err != nil {
		return errors.Wrapf(err, "failed to fix git config")
	}

	// add current content of the store
	if err := s.gitAdd(s.path); err != nil {
		return errors.Wrapf(err, "failed to add '%s' to git", s.path)
	}
	if err := s.gitCommit("Add current content of password store."); err != nil {
		return errors.Wrapf(err, "failed to commit changes to git")
	}

	if err := ioutil.WriteFile(filepath.Join(s.path, ".gitattributes"), []byte("*.gpg diff=gpg\n"), fileMode); err != nil {
		return errors.Errorf("Failed to initialize git: %s", err)
	}
	if err := s.gitAdd(s.path + "/.gitattributes"); err != nil {
		fmt.Println(color.YellowString("Warning: Failed to add .gitattributes to git"))
	}
	if err := s.gitCommit("Configure git repository for gpg file diff."); err != nil {
		fmt.Println(color.YellowString("Warning: Failed to commit .gitattributes to git"))
	}

	// set GPG signkey
	if err := s.gitSetSignKey(signKey); err != nil {
		color.Yellow("Failed to configure Git GPG Commit signing: %s\n", err)
	}

	return nil
}

func (s *Store) gitSetSignKey(sk string) error {
	if sk == "" {
		return errors.Errorf("SignKey not set")
	}

	if err := s.gitCmd("gitSetSignKey", "config", "--local", "user.signingkey", sk); err != nil {
		return errors.Wrapf(err, "failed to set git sign key")
	}

	return s.gitCmd("gitSetSignKey", "config", "--local", "commit.gpgsign", "true")
}

// GitVersion returns the git version as major, minor and patch level
func (s *Store) GitVersion() semver.Version {
	v := semver.Version{}

	cmd := exec.Command("git", "version")
	out, err := cmd.Output()
	if err != nil {
		if gd := os.Getenv("GOPASS_DEBUG"); gd != "" {
			fmt.Printf("[DEBUG] Failed to run 'git version': %s\n", err)
		}
		return v
	}
	svStr := strings.TrimPrefix(string(out), "git version ")
	if p := strings.Fields(svStr); len(p) > 0 {
		svStr = p[0]
	}
	sv, err := semver.ParseTolerant(svStr)
	if err != nil {
		if gd := os.Getenv("GOPASS_DEBUG"); gd != "" {
			fmt.Printf("[DEBUG] Failed to parse '%s' as semver: %s\n", svStr, err)
		}
		return v
	}
	return sv
}

// Git runs arbitrary git commands on this store
func (s *Store) Git(args ...string) error {
	// special case for push, as the gitPush method handles more cases
	if len(args) > 0 && (args[0] == "push" || args[0] == "pull") {
		remote := ""
		if len(args) > 1 {
			remote = args[1]
		}
		branch := ""
		if len(args) > 2 {
			branch = args[2]
		}
		return s.gitPushPull(args[0], remote, branch)
	}
	return s.gitCmd("Git", args...)
}

// isGit returns true if this stores has an (probably) initialized .git folder
func (s *Store) isGit() bool {
	return fsutil.IsFile(filepath.Join(s.path, ".git", "config"))
}

// gitAdd adds the listed files to the git index
func (s *Store) gitAdd(files ...string) error {
	if !s.isGit() {
		return store.ErrGitNotInit
	}
	for i := range files {
		files[i] = strings.TrimPrefix(files[i], s.path+"/")
	}

	args := []string{"add", "--all"}
	args = append(args, files...)

	return s.gitCmd("gitAdd", args...)
}

// gitStagedChanges returns true if there are any staged changes which can be commited
func (s *Store) gitStagedChanges() bool {
	if err := s.gitCmd("gitDiffIndex", "diff-index", "--quiet", "HEAD"); err != nil {
		return true
	}
	return false
}

// gitCommit creates a new git commit with the given commit message
func (s *Store) gitCommit(msg string) error {
	if !s.isGit() {
		return store.ErrGitNotInit
	}

	if !s.gitStagedChanges() {
		return store.ErrGitNothingToCommit
	}

	return s.gitCmd("gitCommit", "commit", "-m", msg)
}

func (s *Store) gitConfigValue(key string) (string, error) {
	if !s.isGit() {
		return "", store.ErrGitNotInit
	}

	buf := &bytes.Buffer{}

	cmd := exec.Command("git", "config", "--get", key)
	cmd.Dir = s.path
	cmd.Stdout = buf
	cmd.Stderr = os.Stderr

	if s.debug {
		fmt.Printf("store.gitConfigValue: %s %+v\n", cmd.Path, cmd.Args)
	}
	if err := cmd.Run(); err != nil {
		return "", err
	}

	return strings.TrimSpace(buf.String()), nil
}

// gitPush pushes the repo to it's origin.
// optional arguments: remote and branch
func (s *Store) gitPushPull(op, remote, branch string) error {
	if !s.isGit() {
		return store.ErrGitNotInit
	}

	if remote == "" {
		remote = "origin"
	}
	if branch == "" {
		branch = "master"
	}

	if v, err := s.gitConfigValue("remote." + remote + ".url"); err != nil || v == "" {
		return store.ErrGitNoRemote
	}

	if err := s.gitCmd("gitPush", "pull", remote, branch); err != nil {
		if op == "pull" {
			return err
		}
		fmt.Println(color.YellowString("Failed to pull before git push: %s", err))
	}
	if op == "pull" {
		return nil
	}

	return s.gitCmd("gitPush", "push", remote, branch)
}

func (s *Store) gitPush(remote, branch string) error {
	return s.gitPushPull("push", remote, branch)
}
