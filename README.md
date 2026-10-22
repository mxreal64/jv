# jv

A fast, drop-in build tool that sits beside Maven and Gradle. One static Go binary; dependency
resolution without a JVM; it shares `~/.m2` with Maven. Whatever jv cannot reproduce faithfully it
hands to the real `mvn` or `gradle` unchanged, so adopting it never blocks a build.

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
  with `-pl` cleans only those modules.
- **`~/.m2/settings.xml`**: mirrors, credentials, `localRepository`, `<profiles>` (repositories and
  properties), `<proxies>`; POM-declared repositories; SHA-1 verification.
- **Build steps**: parallel downloads (stalled transfers abort after 60 s, large files have no overall
  limit), a resolution cache (`.jv/lock.json`), skip-if-unchanged `javac` (source size/mtime based),
  resource copying, JUnit Platform tests (the launcher is fetched automatically, XML reports go to
  `target/surefire-reports`), reproducible jars with directory entries, `install` into the local repository.
- **Compiler settings**: `maven.compiler.*` properties and `maven-compiler-plugin`
  `<release>`, `<source>`, `<target>`, `<encoding>`. Annotation processors on the classpath keep working
  on javac 23+ (jv passes `-proc:full` on javac 21+). javac itself is started with C1-only JIT and the
  serial GC for faster startup.
- **Flags**: `-DskipTests`, `-Dmaven.test.skip`, `-Dtest=...`, `-Dmaven.test.failure.ignore=true`.
  `-B -e -ntp -V -am` are accepted and ignored. `-X` also reports whether a build ran natively.

### Delegated to `mvn`
- Plugins and any non-listed goal; packagings other than `jar`/`pom` (war, ear, maven-plugin, ...);
  projects without a readable `pom.xml`.
- Any flag jv doesn't know (`-T`, `-f`, ...).
- POMs, including inherited parent POMs, that customise the build in ways jv can't reproduce:
  `sourceDirectory`/`testSourceDirectory`, `finalName`, non-default or filtered `resources`, compiler
  configuration other than release/source/target/encoding (`compilerArgs`, `annotationProcessorPaths`,
  ...), and any plugin `<executions>` or plugin configuration (shade, spring-boot repackage, ...).
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
- Encrypted `settings.xml` passwords are not decrypted (use `${env.VAR}` instead).
- Compilation is whole-module, not per-class. Resources deleted from `src/*/resources` stay in
  `target/` until the next recompile or `clean`.
- Plugin-driven code generation and resource filtering are delegated (see above).
- `<module>` entries that point at a non-default POM file name.
- `clean` always runs before the other goals, whatever order they were typed in.
- Test discovery uses the JUnit console launcher's defaults (`*Test`, `*Tests`, `Test*`), not
  Surefire's include/exclude configuration; TestNG is not supported natively.
- Classpath order follows Maven's tree order but is not guaranteed identical in every edge case
  (the set of jars is).

## Testing
`./test-all.sh` is the single entry point (see `--help`):

| Tier | What runs |
|---|---|
| `--quick` | gofmt, `go vet`, `go test -race` (parser, versions, snapshots, settings, packager, compiler helpers, CLI, Gradle model and bridge, resolution semantics against an in-process fake repository), cross-compiles for linux/darwin/windows |
| default | quick + end-to-end scenarios against Maven Central: incremental builds, tests passing/failing, compile errors, scopes, exclusions, version ranges, profiles, reactor builds with BOM + parent POMs, `install` then consuming the installed artifacts, offline mode, error messages, delegation, SIGTERM safety, concurrent runs sharing a cold repository, large real dependency graphs, and Gradle (a stub `gradle` that answers the model query, so no Gradle is needed: model caching and invalidation, `-D`, package/clean, fallback and exit codes, multi-project builds; plus a real-Gradle run when Gradle is installed) |
| `--full` | default + heavier graphs (hadoop, hibernate, netty, aws sdk) + a corpus of real open-source projects |
| `--corpus-only` | just the corpus |

If `mvn` is installed, every classpath check also diffs `jv` against `mvn dependency:build-classpath` for the
compile, runtime and test scopes, and the corpus compares compile output with Maven's. That parity check is the
real definition of "correct" for this tool: run it over more projects before each release
(`--corpus-file FILE` takes `name|git url|ref|subdir|cp|build` lines).

## Build and release
`make build` (Go 1.22+, `CGO_ENABLED=0` so the binary is static). Releases: tag `vX.Y.Z`; CI publishes
binaries, the license and checksums via GoReleaser.

## License
jv is free software, licensed under the **GNU General Public License, version 3 only**
(SPDX: `GPL-3.0-only`); see [LICENSE](LICENSE). Copyright (C) 2026 jv contributors. Every source file
carries an SPDX header, which CI checks. Contributions are accepted under the same license.
