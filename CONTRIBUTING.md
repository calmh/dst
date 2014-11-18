# Hi!

All contributions are welcome! For issues, please file them in the
GitHub issue tracker. For code, please file a GitHub pull request.
Following the guidelines below will smooth out the process of getting a
pull request accepted.

## Coding Style

- Follow the conventions laid out in [Effective Go](https://golang.org/doc/effective_go.html)
  as much as makes sense.

- All text files use Unix line endings.

- Each commit should be `go fmt` clean.

- The commit message subject should be a single short sentence
  describing the change, starting with a capital letter.

- Commits that resolve an existing issue must include the issue number
  as `(fixes #123)` at the end of the commit message subject.

- Imports are grouped per `goimports` standard; that is, standard
  library first, then third party libraries after a blank line.

- A contribution solving a single issue or introducing a single new
  feature should probably be a single commit based on the current
  `master` branch. You may be asked to "rebase" or "squash" your pull
  request to make sure this is the case, especially if there have been
  amendments during review. 

## Licensing

All contributions are made under the same MIT license as the rest of the
project. You retain the copyright to code you have written.

When accepting your first contribution, the maintainer of the project
will ensure that you are added to the AUTHORS file. You are welcome
to add yourself as a separate commit in your first pull request.

## Documentation

http://godoc.org/github.com/calmh/dst

## License

MIT

