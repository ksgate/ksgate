## Release steps

1. Run `go mod tidy` to update the go.mod and go.sum files.
1. Run `make test` to ensure all tests pass.
1. Run `make changelog` to update the CHANGELOG.md file.
1. Update `CHANGELOG.md` file replacing the `## HEAD` section with the new release version.
1. Commit the changes to the CHANGELOG.md file
    ```shell
    git add CHANGELOG.md
    git commit -m "Prepare release"
    ```
1. Tag the release
    ```shell
    git tag -a v0.1.0 -m "Release 0.1.0"
    ```
1. Push the changes
    ```shell
    git push origin v0.1.0
    ```

## TODO

- create docker publish github actions workflow using`make docker-push`
- create a helm chart