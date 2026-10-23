<h1 align="center">jv</h1>

<p align="center">
  <b>Maven and Gradle builds, minus the wait.</b><br>
  One small static binary that resolves, compiles, tests and packages your project &mdash;<br>
  and hands anything it can't reproduce exactly back to the real tool.
</p>

<p align="center">
  <a href="https://github.com/mxreal64/jv/actions/workflows/ci.yml"><img alt="CI" src="https://github.com/mxreal64/jv/actions/workflows/ci.yml/badge.svg"></a>
  <a href="https://github.com/mxreal64/jv/releases"><img alt="Release" src="https://img.shields.io/github/v/release/mxreal64/jv?include_prereleases&sort=semver"></a>
  <a href="LICENSE"><img alt="License: GPL-3.0-only" src="https://img.shields.io/badge/license-GPL--3.0--only-blue.svg"></a>
  <img alt="Go 1.22+" src="https://img.shields.io/badge/go-1.22%2B-00ADD8.svg">
</p>

```console
$ cd my-project            # any Maven or Gradle project, untouched
$ jv test                  # resolve, compile, run your JUnit tests
$ jv package               # target/my-project-1.0.jar
```

## Why jv

- **Drop-in.** It reads the `pom.xml` or Gradle build you already have. No config, no migration, no
  lock-in: delete `jv` and nothing about your project has changed. It shares `~/.m2` with Maven.
- **Never silently different.** When a build uses something jv can't reproduce exactly (shade,
  resource filtering, plugin executions, Kotlin, ...), jv tells you why and runs the real `mvn` or
  `gradle` instead. You get the same result, just not the speed-up.
- **Checked against Maven.** The test suite diffs jv's compile, runtime and test classpaths with
  `mvn dependency:build-classpath` on real dependency graphs (Spring Boot, Maven core, Jackson,
  HttpClient, Log4j, and more).
- **Fast where it counts.** Parallel, checksum-verified downloads, a cached resolution, `javac` skipped
  when nothing changed, no JVM needed to resolve dependencies. For Gradle it asks Gradle once for a
  model and then doesn't start it again until your build scripts change.
- **One static binary.** Linux, macOS and Windows on amd64 and arm64. The only thing it needs is a JDK.
- **Free software.** GPL-3.0.

## Install

**Download a release.** Grab the archive for your platform from the
[Releases page](https://github.com/mxreal64/jv/releases), extract it, and put `jv` on your `PATH`
(Windows: unzip and add the folder to `PATH`). Every release includes `checksums.txt`:

```sh
sha256sum -c checksums.txt --ignore-missing      # Linux
shasum -a 256 jv_*_darwin_*.tar.gz               # macOS: compare the hash with checksums.txt
```

**Or build from source** (Go 1.22+):

```sh
go install github.com/mxreal64/jv@latest
```

You need a JDK (`JAVA_HOME` or `PATH`). Maven or Gradle only has to be installed for the builds jv
delegates to them. Check it worked with `jv --version`.

## Try it

```sh
jv test              # resolve, compile and run the JUnit tests
jv package           # test, then build target/<artifactId>-<version>.jar
jv install           # ...and install it into ~/.m2
jv classpath test    # print the test classpath (IDEs, scripts, debugging)
jv tree              # the resolved dependency tree
jv -pl app test      # one module of a multi-module build, plus the siblings it needs
jv clean install     # goals run in the order you type them
jv -X test           # also say whether the build ran natively or was delegated
```

Want it for every build? `alias mvn=jv` in your shell: anything jv doesn't handle goes to the real
`mvn`. (Use an alias, not a symlink.)

## How it decides

```
jv <goal>
 |
 |-- pom.xml ----------> can jv reproduce this build exactly?
 |                         |-- yes --> native: resolve -> javac -> JUnit -> jar
 |                         '-- no  --> "delegating to mvn because ..."   (your build, unchanged)
 |
 '-- build.gradle[.kts] -> ask Gradle once for a model (cached in .jv/)
                           |-- reproducible --> the same native steps
                           '-- otherwise    --> Gradle itself, arguments translated
```

jv prefers a loud hand-off to a quiet difference. See *Maven projects* and *Gradle projects* below for
exactly what runs natively and what is delegated.

## FAQ

**Is this a Maven or Gradle replacement?** No, it's a companion. It speeds up the everyday loop
(resolve, compile, test, package, install) and leaves everything else (plugins, releases, site
generation, code generation) to the real tool.

**Will it produce a different result than Maven?** It is built not to. Classpaths are compared with
Maven's in the test suite, jars are reproducible, and anything unfamiliar is delegated. If you ever
see a difference, that's a bug: please open an issue.

**What about plugins?** Plugin goals and builds that depend on plugin executions run through the
real tool. The compiler and Surefire configuration jv understands is honoured natively.

**Does it work on Windows?** Yes. The release includes Windows binaries and CI runs the test suite on
Windows, macOS and Linux.

**How mature is it?** It is pre-1.0. The core paths are well covered by tests, and the known gaps are
listed under *Known limitations*. Try it on a branch before you rely on it in CI.

## Usage reference

```
jv compile | test | package | install | clean
jv classpath [compile|runtime|test]     jv tree
jv -o -q -X -U -P profile -Dkey=value -pl module1,module2 -s settings.xml <goal>
jv --version | -h
```

`jv` picks the build system for you: a `pom.xml` means Maven; otherwise a Gradle build
(`build.gradle[.kts]`, `settings.gradle[.kts]` or `gradlew`, in the directory or an ancestor) means Gradle.

## Maven projects

### Done natively
- **Dependency resolution** with Maven semantics: parent POMs, `${}` interpolation (including inside
  property values), `dependencyManagement`, BOM imports, exclusions, optional dependencies, scope
  mediation (a widened scope also widens the transitive children), nearest-wins conflicts, version
  ranges, and an exact port of Maven's `ComparableVersion` ordering.
- **Profiles**: `-P`/`-P!id`, `activeByDefault`, and activation by property, `<jdk>`, `<os>` and `<file>`;
  modules declared inside profiles are part of the reactor.
- **Remote `-SNAPSHOT` dependencies** (unique and non-unique layouts); `-U` re-checks them.
- **Multi-module reactors** in dependency order; siblings resolve to their build output. `-pl a,b`
  builds the named modules plus the siblings they need (so `-am` is accepted and ignored) and `clean`
  with `-pl` cleans only those modules. A `<module>` may name a non-default POM file
  (`<module>sub/alt.xml</module>`); `install` publishes that file.
- **`clean` in any position**: a leading `clean` takes a fast path that needs no network or POM
  resolution; a `clean` anywhere else (`jv install clean`) runs at its position in the goal list.
- **`~/.m2/settings.xml`**: mirrors, credentials, `localRepository`, `<profiles>` (repositories and
  properties), `<proxies>`; POM-declared repositories; SHA-1 verification. Encrypted server passwords
  (`{...}`) are decrypted with the master password in `~/.m2/settings-security.xml` (see Repositories
  and security).
- **Build steps**: parallel downloads (stalled transfers abort after 60 s, large files have no overall
  limit), a resolution cache (`.jv/lock.json`), skip-if-unchanged `javac` (source size/mtime based),
  resource copying that also removes resources you deleted from `src/*/resources` (tracked in
  `.jv-resources`; compiled classes are never touched), JUnit Platform tests (the launcher is fetched
  automatically, XML reports go to `target/surefire-reports`), reproducible jars with directory
  entries, `install` into the local repository.
- **Compiler settings**: `maven.compiler.*` properties and `maven-compiler-plugin`
  `<release>`, `<source>`, `<target>`, `<encoding>`. Annotation processors on the classpath keep working
  on javac 23+ (jv passes `-proc:full` on javac 21+). javac itself is started with C1-only JIT and the
  serial GC for faster startup.
- **Plugin management**: `<pluginManagement>` entries for `maven-compiler-plugin`,
  `maven-surefire-plugin` and `maven-jar-plugin` are read (child entries replace the parent's per
  artifactId), because Maven applies them to the default lifecycle without the plugin being declared.
- **Test selection (`maven-surefire-plugin`)**: `<includes>`/`<excludes>` (Ant globs such as
  `**/*IT.java`; explicit includes replace the defaults `**/Test*.java`, `**/*Test.java`,
  `**/*Tests.java`, `**/*TestCase.java`; nested and anonymous classes are never test classes),
  `<groups>`/`<excludedGroups>` (JUnit tags), `<argLine>` and `<systemPropertyVariables>`.
  `-Dtest=...` overrides includes/excludes, as in Surefire.
- **Flags**: `-DskipTests`, `-Dmaven.test.skip`, `-Dtest=...`, `-Dmaven.test.failure.ignore=true`.
  `-B -e -ntp -V -am` are accepted and ignored. `-X` also reports whether a build ran natively.

### Delegated to `mvn`
- Plugins and any non-listed goal; packagings other than `jar`/`pom` (war, ear, maven-plugin, ...);
  projects without a readable `pom.xml`.
- Any flag jv doesn't know (`-T`, `-f`, ...).
- POMs, including inherited parent POMs, that customise the build in ways jv can't reproduce:
  `sourceDirectory`/`testSourceDirectory`, `finalName`, non-default or filtered `resources`, compiler
  configuration other than release/source/target/encoding (`compilerArgs`, `annotationProcessorPaths`,
  ...), surefire configuration beyond the list above (`forkCount`, `parallel`, ...), `%regex[...]`
  or `%ant[...]` test patterns, an `<argLine>` that uses `@{...}` late binding (jacoco), the
  `maven-jar-plugin` configuration (including from `<pluginManagement>`), and any plugin
  `<executions>` or plugin configuration (shade, spring-boot repackage, ...).
  jv prints which setting caused the delegation. `JV_IGNORE_BUILD_CONFIG=1` builds natively anyway
  (a warning is printed instead).

## Gradle projects

jv does not re-implement the Groovy/Kotlin DSL. It asks a real Gradle (the wrapper if present) **once**
for a JSON model through an init script (source dirs, resolved compile/runtime/test classpaths, javac and
test settings), caches it in `.jv/gradle-model.json` keyed by the build scripts, `gradle.properties`,
version catalogs and `-P/-D` arguments, then compiles, tests and packages natively. Later runs do not
start Gradle at all.

- **Native**: `clean`, `compile`, `test`, `package` (like `build`: it runs the tests), `classpath`.
  `classpath` names exactly one project (`-pl`, or the project you are in).
- **Through Gradle** (the bridge): everything else, and any build the model flags as not reproducible
  (Kotlin/Groovy/Scala, war, annotation processors, several source dirs, TestNG, extra tasks wired into
  `compileJava`, non-core plugins, a project-level dependency cycle). `JV_GRADLE_ORACLE=0` always uses
  the bridge; `JV_IGNORE_BUILD_CONFIG=1` builds natively despite the warnings.
- **Bridge translation**: `compile`→`classes`, `test`→`test`, `package`→`build`,
  `install`→`publishToMavenLocal`, `tree`→`dependencies`, `-DskipTests`→`-x test`, `-o`→`--offline`,
  `-X`→`--info`, `-pl a,b`→`:a:<task> :b:<task>`; `-s settings.xml` is dropped (it has no Gradle equivalent).
  Gradle's exit code is returned unchanged.
- **Safety**: `jv clean` refuses to delete a build directory that is, or contains, the project or its
  sources (a script can set `buildDir = projectDir`); nothing is deleted if any target looks wrong.

## Repositories and security
- Credentials from `settings.xml` are only sent to Maven Central, settings mirrors, and repositories
  declared in the project's own POMs, never to repositories that only a dependency's POM declared.
- Plain-`http` repositories declared in POMs are ignored (as in Maven 3.8.1+); `localhost` is allowed
  and an `http` mirror configured in `settings.xml` is honoured.
- Downloads are verified against the repository's `.sha1` when it provides one (`-X` shows artifacts
  that couldn't be verified). That detects corruption, not a malicious repository;
  `JV_REQUIRE_CHECKSUMS=1` refuses artifacts without a checksum.
- **Encrypted passwords**: a server `<password>` of the form `{...}` is decrypted with the master
  password from `~/.m2/settings-security.xml` (itself encrypted, as written by
  `mvn --encrypt-master-password`). jv implements Maven's plexus-cipher AES scheme. A test
  (`config/decrypt_maven_test.go`) encrypts with your installed `mvn` and checks that jv decrypts it;
  it is skipped when `mvn` is absent, and a failure means the cipher layout differs from that Maven
  version. If decryption fails at build time jv warns once and sends **no** credentials rather than
  a wrong password. The decrypted password lives in memory only and is never logged.

## Environment
| Variable | Meaning |
|---|---|
| `JV_REPO` | local repository (overrides `settings.xml`; relative paths are resolved at startup) |
| `JV_JUNIT_VERSION` | `junit-platform-console-standalone` version to fetch (default `1.10.2`) |
| `JV_IGNORE_BUILD_CONFIG` | build natively even if the POM/Gradle model has settings jv ignores |
| `JV_GRADLE_ORACLE` | `0` = never use the native Gradle path, always run Gradle itself |
| `JV_REQUIRE_CHECKSUMS` | refuse artifacts whose repository provides no `.sha1` |
| `JAVA_HOME` | JDK used for `javac`/`java` (wins over `PATH`, like Maven and Gradle) |

## Known limitations
- `-f`/`--file` is not handled natively (it is delegated to `mvn`); a non-default POM is only
  supported as a `<module>` entry.
- Password decryption reads `~/.m2/settings-security.xml` only (no `-Dsettings.security`, no
  `<relocation>` chains beyond one hop) and covers server passwords, not proxy passwords.
- Surefire: only Ant-style `includes`/`excludes`; a pattern without a leading `**/` matches the default
  package only (as in Surefire). An `<argLine>` that still contains `${...}` after interpolation
  drops those tokens. `groups` are passed as JUnit tags (comma = OR).
- Compilation is whole-module, not per-class.
- Plugin-driven code generation and resource filtering are delegated (see above).
- `clean` always takes the fast path when it is the first goal; `jv clean install` therefore cleans
  every module before any POM is resolved (which is also what Maven does).
- Goals run goal-by-goal across all modules (Maven runs module-by-module); results are the same for
  successful builds, log order and failure behaviour differ.
- TestNG is not supported natively; test discovery uses JUnit Platform engines found on the classpath.
- Classpath order follows Maven's tree order but is not guaranteed identical in every edge case
  (the set of jars is).

## Testing
`./test-all.sh` is the single entry point (see `--help`):

| Tier | What runs |
|---|---|
| `--quick` | gofmt, `go vet`, `go test -race` (parser, plugin config, versions, snapshots, settings and password decryption, packager, resource syncing, test-selection patterns, module discovery, CLI, Gradle model and bridge, resolution semantics against an in-process fake repository), cross-compiles for linux/darwin/windows |
| default | quick + end-to-end scenarios against Maven Central: incremental builds, tests passing/failing, compile errors, scopes, exclusions, version ranges, profiles, reactor builds with BOM + parent POMs, `install` then consuming the installed artifacts, offline mode, error messages, delegation, SIGTERM safety, concurrent runs sharing a cold repository, large real dependency graphs, and Gradle (a stub `gradle` that answers the model query, so no Gradle is needed: model caching and invalidation, `-D`, package/clean, fallback and exit codes, multi-project builds; plus a real-Gradle run when Gradle is installed) |
| `--full` | default + heavier graphs (hadoop, hibernate, netty, aws sdk) + a corpus of real open-source projects |
| `--corpus-only` | just the corpus |

If `mvn` is installed, every classpath check also diffs `jv` against `mvn dependency:build-classpath` for the
compile, runtime and test scopes, and the corpus compares compile output with Maven's. That parity check is the
real definition of "correct" for this tool: run it over more projects before each release
(`--corpus-file FILE` takes `name|git url|ref|subdir|cp|build` lines).

## Build and release
`make build` (Go 1.22+, `CGO_ENABLED=0` so the binary is static) or
`go install github.com/mxreal64/jv@latest`. Releases: tag `vX.Y.Z` (use `vX.Y.Z-rc1` to rehearse); CI
runs the tests and then GoReleaser publishes the binaries, the license and checksums.

## Contributing
Bug reports and pull requests are welcome; see [CONTRIBUTING.md](CONTRIBUTING.md). Run
`./test-all.sh --quick` before sending a change (gofmt, vet, unit tests, cross-compiles), and
`./test-all.sh` for anything that affects resolution or builds. Report security problems privately
(see [SECURITY.md](SECURITY.md)).

## License
jv is free software, licensed under the **GNU General Public License, version 3 only**
(SPDX: `GPL-3.0-only`); see [LICENSE](LICENSE). Copyright (C) 2026 jv contributors. Every source file
carries an SPDX header, which CI checks. Contributions are accepted under the same license.

Earlier revisions of this repository were published under the MIT License; copies obtained under it
stay available under it. jv runs `mvn`, `gradle`, `java` and `javac` as separate programs and downloads
the JUnit console launcher at runtime; none of them is linked into or shipped with jv.
