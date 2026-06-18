**issue**
Running `go mod tidy` during Slice B downloaded several modules not obviously related to the project (`github.com/google/go-cmp`, `golang.org/x/tools`, `github.com/golang-jwt/jwt/v5`), raising a brief concern about dependency creep or an unintended SDK change. Encountered during Slice B.

**fixed**
Confirmed these are legitimate transitive (indirect/test) dependencies of the pinned go-sdk v1.4.1, verified `git diff go.mod` showed no change to the SDK pin, and that the set of direct dependencies was unchanged. No code change was required beyond the confirmation; recorded here so the dependency surface is understood and the next person does not re-investigate.
