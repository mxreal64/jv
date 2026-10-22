#!/usr/bin/env bash
# bench.sh — benchmark jv against mvn, mvnd and Gradle
#
# Usage:
#   ./bench.sh [options]
#
# Options:
#   --jv PATH        path to jv binary (default: ./jv then jv on PATH)
#   --runs N         timed runs per scenario (default: 7)
#   --warmup N       warmup runs before timing (default: 2)
#   --out FILE       markdown report path (default: bench-results.md)
#   --project DIR    benchmark an existing project instead of the generated one
#   --no-gradle      skip Gradle scenarios
#   --no-maven       skip Maven scenarios
#   --no-mvnd        skip Maven Daemon scenarios
#   --help
#
# Requirements: bash 4+, java, javac.
# Optional: mvn, mvnd, gradle/gradlew (skipped gracefully when absent).

set -euo pipefail

# ── defaults ─────────────────────────────────────────────────────────────────
RUNS=7
WARMUP=2
OUT="bench-results.md"
JV_BIN=""
PROJECT_DIR=""
SKIP_GRADLE=0
SKIP_MAVEN=0
SKIP_MVND=0

# ── colours ──────────────────────────────────────────────────────────────────
if [ -t 1 ]; then
  BLD='\033[1m' GRN='\033[32m' YEL='\033[33m' CYN='\033[36m' RST='\033[0m'
else
  BLD='' GRN='' YEL='' CYN='' RST=''
fi

# ── arg parsing ───────────────────────────────────────────────────────────────
while [[ $# -gt 0 ]]; do
  case $1 in
    --jv)        JV_BIN="$2";      shift 2 ;;
    --runs)      RUNS="$2";        shift 2 ;;
    --warmup)    WARMUP="$2";      shift 2 ;;
    --out)       OUT="$2";         shift 2 ;;
    --project)   PROJECT_DIR="$2"; shift 2 ;;
    --no-gradle) SKIP_GRADLE=1;    shift   ;;
    --no-maven)  SKIP_MAVEN=1;     shift   ;;
    --no-mvnd)   SKIP_MVND=1;      shift   ;;
    --help|-h)
      sed -n '3,15p' "$0" | sed 's/^# \?//'
      exit 0 ;;
    *) echo "unknown option: $1"; exit 1 ;;
  esac
done

# ── locate tools ──────────────────────────────────────────────────────────────
find_tool() { command -v "$1" 2>/dev/null || true; }

if [ -z "$JV_BIN" ]; then
  if [ -x "./jv" ]; then
    JV_BIN="$(pwd)/jv"
  else
    JV_BIN=$(find_tool jv)
  fi
fi
[ -z "$JV_BIN" ] && { echo "error: jv not found; pass --jv PATH"; exit 1; }
# Make absolute so it works from any working directory
case "$JV_BIN" in
  /*) ;;
  *)  JV_BIN="$(cd "$(dirname "$JV_BIN")" && pwd)/$(basename "$JV_BIN")" ;;
esac

MVN=""
MVND=""
GRADLE=""
[ "$SKIP_MAVEN"  = 0 ] && MVN=$(find_tool mvn)
[ "$SKIP_MVND"   = 0 ] && MVND=$(find_tool mvnd)
[ "$SKIP_GRADLE" = 0 ] && GRADLE=$(find_tool gradle)

JV_VER=$("$JV_BIN" --version 2>/dev/null | head -1 || echo "jv (unknown)")
MVN_VER=$([ -n "$MVN" ]    && "$MVN"    -v 2>/dev/null | head -1 || true)
MVND_VER=$([ -n "$MVND" ]  && "$MVND"   -v 2>/dev/null | head -1 || true)
GRADLE_VER=$([ -n "$GRADLE" ] && "$GRADLE" --version 2>/dev/null | grep "^Gradle" | head -1 || true)
JAVA_VER=$(java -version 2>&1 | head -1)
OS_INFO=$(uname -srm)

section() { printf "\n${BLD}── %s ──${RST}\n" "$1"; }
info()    { printf "  ${CYN}%s${RST}\n" "$1"; }
ok()      { printf "  ${GRN}✓${RST} %s\n" "$1"; }
warn()    { printf "  ${YEL}!${RST} %s\n" "$1"; }

section "Environment"
info "$JV_VER   ($JV_BIN)"
[ -n "$MVN_VER"    ] && info "$MVN_VER"    || warn "mvn not found — Maven scenarios skipped"
[ -n "$MVND_VER"   ] && info "$MVND_VER"   || warn "mvnd not found — Maven Daemon scenarios skipped"
[ -n "$GRADLE_VER" ] && info "$GRADLE_VER" || warn "gradle not found — Gradle scenarios skipped"
info "Java:  $JAVA_VER"
info "OS:    $OS_INFO"

# ── project setup ─────────────────────────────────────────────────────────────
WORK=$(mktemp -d "${TMPDIR:-/tmp}/jv-bench.XXXXXX")
trap 'rm -rf "$WORK"' EXIT

make_project() {
  local dir="$1"
  mkdir -p "$dir/src/main/java/bench" "$dir/src/test/java/bench"

  cat > "$dir/pom.xml" << 'POM'
<project>
  <modelVersion>4.0.0</modelVersion>
  <groupId>bench</groupId>
  <artifactId>bench-app</artifactId>
  <version>1.0-SNAPSHOT</version>
  <properties>
    <maven.compiler.release>17</maven.compiler.release>
    <project.build.sourceEncoding>UTF-8</project.build.sourceEncoding>
  </properties>
  <dependencies>
    <dependency>
      <groupId>com.google.guava</groupId>
      <artifactId>guava</artifactId>
      <version>32.1.3-jre</version>
    </dependency>
    <dependency>
      <groupId>com.fasterxml.jackson.core</groupId>
      <artifactId>jackson-databind</artifactId>
      <version>2.17.1</version>
    </dependency>
    <dependency>
      <groupId>org.junit.jupiter</groupId>
      <artifactId>junit-jupiter-api</artifactId>
      <version>5.10.2</version>
      <scope>test</scope>
    </dependency>
    <dependency>
      <groupId>org.junit.jupiter</groupId>
      <artifactId>junit-jupiter-engine</artifactId>
      <version>5.10.2</version>
      <scope>test</scope>
    </dependency>
  </dependencies>
</project>
POM

  # small project (5 files): measures startup, not javac
  for i in 1 2 3 4 5; do
    cat > "$dir/src/main/java/bench/Util${i}.java" << JAVA
package bench;
public class Util${i} {
    public static int value() { return ${i}; }
}
JAVA
  done

  cat > "$dir/src/main/java/bench/App.java" << 'JAVA'
package bench;
public class App {
    public static void main(String[] args) {
        System.out.println("bench " + Util1.value());
    }
}
JAVA

  cat > "$dir/src/test/java/bench/AppTest.java" << 'JAVA'
package bench;
import org.junit.jupiter.api.Test;
import static org.junit.jupiter.api.Assertions.assertEquals;
class AppTest {
    @Test void util1() { assertEquals(1, Util1.value()); }
    @Test void util2() { assertEquals(2, Util2.value()); }
}
JAVA

  # Gradle build alongside pom.xml for Gradle scenarios
  cat > "$dir/settings.gradle" << 'GRADLE'
rootProject.name = 'bench-app'
GRADLE

  cat > "$dir/build.gradle" << 'GRADLE'
plugins { id 'java' }
group = 'bench'; version = '1.0-SNAPSHOT'
repositories { mavenCentral() }
java { toolchain { languageVersion = JavaLanguageVersion.of(17) } }
dependencies {
    implementation 'com.google.guava:guava:32.1.3-jre'
    implementation 'com.fasterxml.jackson.core:jackson-databind:2.17.1'
    testImplementation 'org.junit.jupiter:junit-jupiter-api:5.10.2'
    testRuntimeOnly    'org.junit.jupiter:junit-jupiter-engine:5.10.2'
}
test { useJUnitPlatform() }
GRADLE
}

if [ -n "$PROJECT_DIR" ]; then
  PROJ="$PROJECT_DIR"
  info "using existing project: $PROJ"
else
  PROJ="$WORK/proj"
  make_project "$PROJ"
  ok "generated benchmark project (6 source files, 2 tests) in $PROJ"
fi

# ── timing ────────────────────────────────────────────────────────────────────
now_ms() {
  if [ -n "${EPOCHREALTIME:-}" ]; then
    # bash 5: EPOCHREALTIME = seconds.microseconds
    awk "BEGIN{printf \"%d\", ${EPOCHREALTIME/,/.} * 1000}"
  elif command -v python3 >/dev/null 2>&1; then
    python3 -c 'import time; print(int(time.time()*1000))'
  else
    echo $(( $(date +%s) * 1000 ))
  fi
}

# time_cmd <warmup> <runs> cmd...
# stdout: "min med max" in ms
time_cmd() {
  local warmup=$1 runs=$2; shift 2
  local i ms times min med max sorted
  for (( i=0; i<warmup; i++ )); do "$@" >/dev/null 2>&1 || true; done
  times=()
  for (( i=0; i<runs; i++ )); do
    local t0 t1
    t0=$(now_ms)
    "$@" >/dev/null 2>&1 || true
    t1=$(now_ms)
    times+=( $(( t1 - t0 )) )
  done
  sorted=( $(printf '%s\n' "${times[@]}" | sort -n) )
  min=${sorted[0]}
  max=${sorted[-1]}
  med=${sorted[$(( runs / 2 ))]}
  echo "$min $med $max"
}

# ── result store ──────────────────────────────────────────────────────────────
declare -a ROW_TOOL ROW_SCEN ROW_MIN ROW_MED ROW_MAX ROW_NOTE

record() {
  local tool=$1 scen=$2 min=$3 med=$4 max=$5 note=${6:-}
  ROW_TOOL+=("$tool"); ROW_SCEN+=("$scen")
  ROW_MIN+=("$min");   ROW_MED+=("$med"); ROW_MAX+=("$max"); ROW_NOTE+=("$note")
}
record_skip() {
  ROW_TOOL+=("$1"); ROW_SCEN+=("$2")
  ROW_MIN+=("—"); ROW_MED+=("—"); ROW_MAX+=("—"); ROW_NOTE+=("${3:-skipped}")
}

# ── settings.xml: an empty but valid file so mvn/mvnd don't hit ~/.m2/settings.xml ──
SETTINGS="$WORK/settings.xml"
cat > "$SETTINGS" << 'XML'
<settings xmlns="http://maven.apache.org/SETTINGS/1.2.0"></settings>
XML

# ── shared repo (populated once, reused across warm scenarios) ────────────────
SHARED_M2="$WORK/m2-shared"
mkdir -p "$SHARED_M2"
info "pre-populating shared local repo (one-time download)..."
( cd "$PROJ" && JV_REPO="$SHARED_M2" "$JV_BIN" -s "$SETTINGS" -q compile ) >/dev/null 2>&1 || true
ok "shared repo ready"

# ── jv scenarios ──────────────────────────────────────────────────────────────
run_jv() {
  local dir="$1"
  local jv=( "$JV_BIN" -s "$SETTINGS" -q )
  local r

  # 1. COLD RESOLVE: empty cache, empty classes → measures full network + javac
  info "jv: cold resolve..."
  r=$(time_cmd 0 "$RUNS" bash -c "
    rm -rf '$WORK/m2-cold' '$dir/.jv' '$dir/target'
    cd '$dir' && JV_REPO='$WORK/m2-cold' ${jv[*]} compile
  " --)
  record "jv" "cold resolve + compile" $r "empty repo, full download + javac"

  # 2. WARM RESOLVE, PRECOMPILED: repo cached, classes exist → lock hit + stamp check → pure startup
  rm -rf "$dir/.jv" "$dir/target"
  ( cd "$dir" && JV_REPO="$SHARED_M2" "${jv[@]}" compile ) >/dev/null 2>&1 || true
  info "jv: warm / nothing changed (pure startup)..."
  r=$(time_cmd "$WARMUP" "$RUNS" bash -c "
    cd '$dir' && JV_REPO='$SHARED_M2' ${jv[*]} compile
  " --)
  record "jv" "warm: nothing changed (startup + stamp check)" $r "lock hit, classes current → javac skipped"

  # 3. WARM RESOLVE, ONE FILE CHANGED: repo cached, one source file touched → lock hit + incremental javac
  info "jv: warm / one file changed..."
  r=$(time_cmd "$WARMUP" "$RUNS" bash -c "
    touch '$dir/src/main/java/bench/App.java'
    cd '$dir' && JV_REPO='$SHARED_M2' ${jv[*]} compile
  " --)
  record "jv" "warm: one file changed (startup + javac)" $r "lock hit, one source touched → javac runs"

  # 4. WARM RESOLVE, LOCK MISS: classes wiped but repo intact → re-resolve from local repo + javac
  info "jv: warm / lock miss (classes wiped)..."
  r=$(time_cmd "$WARMUP" "$RUNS" bash -c "
    rm -rf '$dir/.jv' '$dir/target'
    cd '$dir' && JV_REPO='$SHARED_M2' ${jv[*]} compile
  " --)
  record "jv" "warm: lock miss (local repo cached, recompile)" $r "no lock, local repo hit, javac runs"

  # 5. FULL BUILD: resolve + compile + test + package, classes wiped, repo cached
  info "jv: full build..."
  r=$(time_cmd "$WARMUP" "$RUNS" bash -c "
    rm -rf '$dir/.jv' '$dir/target'
    cd '$dir' && JV_REPO='$SHARED_M2' ${jv[*]} compile test package
  " --)
  record "jv" "full build: compile + test + package" $r "local repo, no lock"
}

# ── mvn/mvnd scenarios ────────────────────────────────────────────────────────
run_mvn() {
  local tool=$1 cmd=$2 dir=$3
  local base_args=( -s "$SETTINGS" --no-transfer-progress -q -Dmaven.repo.local="$SHARED_M2" )
  local cold_args=( -s "$SETTINGS" --no-transfer-progress -q -Dmaven.repo.local="$WORK/m2-mvn-cold" )
  local r

  # 1. COLD
  info "$tool: cold resolve..."
  r=$(time_cmd 0 "$RUNS" bash -c "
    rm -rf '$WORK/m2-mvn-cold' '$dir/target'
    cd '$dir' && '$cmd' ${cold_args[*]} compile
  " --)
  record "$tool" "cold resolve + compile" $r "empty repo, full download + javac"

  # prime target/ for warm scenarios
  rm -rf "$dir/target"
  ( cd "$dir" && "$cmd" "${base_args[@]}" compile ) >/dev/null 2>&1 || true

  # 2. NOTHING CHANGED
  info "$tool: nothing changed..."
  r=$(time_cmd "$WARMUP" "$RUNS" bash -c "
    cd '$dir' && '$cmd' ${base_args[*]} compile
  " --)
  record "$tool" "warm: nothing changed (JVM startup)" $r "local repo, classes current"

  # 3. ONE FILE CHANGED
  info "$tool: one file changed..."
  r=$(time_cmd "$WARMUP" "$RUNS" bash -c "
    touch '$dir/src/main/java/bench/App.java'
    cd '$dir' && '$cmd' ${base_args[*]} compile
  " --)
  record "$tool" "warm: one file changed (JVM startup + javac)" $r "local repo, one source touched"

  # 4. FULL BUILD (classes wiped, repo cached)
  info "$tool: full build..."
  r=$(time_cmd "$WARMUP" "$RUNS" bash -c "
    rm -rf '$dir/target'
    cd '$dir' && '$cmd' ${base_args[*]} test package
  " --)
  record "$tool" "full build: compile + test + package" $r "local repo, no prior classes"
}

# ── Gradle scenarios ──────────────────────────────────────────────────────────
SHARED_GRADLE_HOME="$WORK/gradle-home"
mkdir -p "$SHARED_GRADLE_HOME"

run_gradle() {
  local tool=$1 cmd=$2 dir=$3
  local g_args=( --no-daemon --console=plain --gradle-user-home "$SHARED_GRADLE_HOME" -q )
  local r

  # pre-populate Gradle cache
  info "$tool: pre-populating Gradle cache..."
  ( cd "$dir" && "$cmd" "${g_args[@]}" classes ) >/dev/null 2>&1 || true

  # 1. COLD (wipe Gradle cache + build dir)
  info "$tool: cold resolve..."
  r=$(time_cmd 0 "$RUNS" bash -c "
    rm -rf '$WORK/gradle-home-cold' '$dir/build'
    cd '$dir' && '$cmd' --no-daemon --console=plain --gradle-user-home '$WORK/gradle-home-cold' -q classes
  " --)
  record "$tool" "cold resolve + compile" $r "empty cache, full download + javac"

  # 2. NOTHING CHANGED (cache warm, classes exist)
  info "$tool: nothing changed..."
  # ensure classes exist
  ( cd "$dir" && "$cmd" "${g_args[@]}" classes ) >/dev/null 2>&1 || true
  r=$(time_cmd "$WARMUP" "$RUNS" bash -c "
    cd '$dir' && '$cmd' ${g_args[*]} classes
  " --)
  record "$tool" "warm: nothing changed (JVM startup)" $r "cache warm, classes current (UP-TO-DATE)"

  # 3. ONE FILE CHANGED
  info "$tool: one file changed..."
  r=$(time_cmd "$WARMUP" "$RUNS" bash -c "
    touch '$dir/src/main/java/bench/App.java'
    cd '$dir' && '$cmd' ${g_args[*]} classes
  " --)
  record "$tool" "warm: one file changed (JVM startup + javac)" $r "cache warm, one source touched"

  # 4. FULL BUILD (build dir wiped, cache warm)
  info "$tool: full build..."
  r=$(time_cmd "$WARMUP" "$RUNS" bash -c "
    rm -rf '$dir/build'
    cd '$dir' && '$cmd' ${g_args[*]} test jar
  " --)
  record "$tool" "full build: compile + test + package" $r "warm cache, no prior build dir"
}

# ── run ───────────────────────────────────────────────────────────────────────
section "Running jv scenarios ($RUNS runs, $WARMUP warmup)"
run_jv "$PROJ"
ok "jv done"

if [ -n "$MVN" ]; then
  section "Running mvn scenarios ($RUNS runs, $WARMUP warmup)"
  run_mvn "mvn" "$MVN" "$PROJ"
  ok "mvn done"
else
  for s in "cold resolve + compile" "warm: nothing changed (JVM startup)" \
            "warm: one file changed (JVM startup + javac)" "full build: compile + test + package"; do
    record_skip "mvn" "$s" "not installed"
  done
fi

if [ -n "$MVND" ]; then
  section "Running mvnd scenarios ($RUNS runs, $WARMUP warmup)"
  # extra warmup to ensure the daemon is hot
  ORIG=$WARMUP; WARMUP=$(( WARMUP + 2 ))
  run_mvn "mvnd" "$MVND" "$PROJ"
  WARMUP=$ORIG
  "$MVND" --stop >/dev/null 2>&1 || true
  ok "mvnd done"
else
  for s in "cold resolve + compile" "warm: nothing changed (JVM startup)" \
            "warm: one file changed (JVM startup + javac)" "full build: compile + test + package"; do
    record_skip "mvnd" "$s" "not installed"
  done
fi

GRADLEW=""
[ -x "$PROJ/gradlew" ] && GRADLEW="$PROJ/gradlew"
GRADLE_CMD="${GRADLEW:-$GRADLE}"

if [ -n "$GRADLE_CMD" ] && [ "$SKIP_GRADLE" = 0 ]; then
  section "Running Gradle (no-daemon) scenarios ($RUNS runs, $WARMUP warmup)"
  run_gradle "gradle --no-daemon" "$GRADLE_CMD" "$PROJ"
  ok "Gradle done"
else
  for s in "cold resolve + compile" "warm: nothing changed (JVM startup)" \
            "warm: one file changed (JVM startup + javac)" "full build: compile + test + package"; do
    record_skip "gradle --no-daemon" "$s" "not installed"
  done
fi

# ── print ─────────────────────────────────────────────────────────────────────
section "Results (ms, median of $RUNS runs)"
printf "\n"
{
  printf "%-30s %-50s %7s %7s %7s\n" "tool" "scenario" "min" "median" "max"
  printf "%-30s %-50s %7s %7s %7s\n" \
    "$(printf '%.0s─' {1..30})" "$(printf '%.0s─' {1..50})" "───────" "───────" "───────"
  prev=""
  for (( i=0; i<${#ROW_TOOL[@]}; i++ )); do
    s="${ROW_SCEN[$i]}"
    [ "$s" != "$prev" ] && [ $i -gt 0 ] && printf "\n"
    if [ -n "${ROW_NOTE[$i]}" ] && [ "${ROW_MIN[$i]}" = "—" ]; then
      printf "%-30s %-50s  (%s)\n" "${ROW_TOOL[$i]}" "$s" "${ROW_NOTE[$i]}"
    else
      printf "%-30s %-50s %7s %7s %7s\n" \
        "${ROW_TOOL[$i]}" "$s" "${ROW_MIN[$i]}" "${ROW_MED[$i]}" "${ROW_MAX[$i]}"
    fi
    prev="$s"
  done
} 2>/dev/null

# ── markdown ──────────────────────────────────────────────────────────────────
{
  echo "# jv benchmark results"
  echo
  echo "Generated: $(date -u '+%Y-%m-%d %H:%M:%S UTC')"
  echo
  echo "## Environment"
  echo
  echo "| | |"
  echo "|---|---|"
  echo "| OS     | \`$OS_INFO\` |"
  echo "| Java   | \`$JAVA_VER\` |"
  echo "| jv     | \`$JV_VER\` |"
  [ -n "$MVN_VER"    ] && echo "| mvn    | \`$MVN_VER\` |"
  [ -n "$MVND_VER"   ] && echo "| mvnd   | \`$MVND_VER\` |"
  [ -n "$GRADLE_VER" ] && echo "| Gradle | \`$GRADLE_VER\` |"
  echo "| Runs   | $RUNS (+ $WARMUP warmup) |"
  echo
  echo "## Results (all times in ms, median of $RUNS runs)"
  echo
  echo "| Tool | Scenario | Min | Median | Max | What it measures |"
  echo "|---|---|---:|---:|---:|---|"
  for (( i=0; i<${#ROW_TOOL[@]}; i++ )); do
    if [ "${ROW_MIN[$i]}" = "—" ]; then
      echo "| ${ROW_TOOL[$i]} | ${ROW_SCEN[$i]} | — | — | — | ${ROW_NOTE[$i]} |"
    else
      echo "| ${ROW_TOOL[$i]} | ${ROW_SCEN[$i]} | ${ROW_MIN[$i]} | ${ROW_MED[$i]} | ${ROW_MAX[$i]} | ${ROW_NOTE[$i]} |"
    fi
  done
  echo
  echo "## What each scenario isolates"
  echo
  echo "| Scenario | What dominates the time |"
  echo "|---|---|"
  echo "| cold resolve + compile | network I/O + POM parsing + javac |"
  echo "| warm: nothing changed | **pure startup cost** — lock hit, stamp check passes, javac skipped |"
  echo "| warm: one file changed | startup + incremental javac on one file |"
  echo "| warm: lock miss | startup + local-repo classpath rebuild + javac |"
  echo "| full build | startup + resolve + javac + JUnit launcher + jar |"
  echo
  echo "## Notes"
  echo
  echo "- Project: 6 source files so javac finishes in ~10 ms and **startup dominates**."
  echo "  The key number is _warm: nothing changed_: how long does the tool take when there"
  echo "  is literally nothing to do? That is the developer inner-loop latency."
  echo "- Cold-resolve runs start with a completely empty local repository."
  echo "- Warm scenarios share a pre-populated \`~/.m2\`-equivalent so network I/O is zero."
  echo "- mvnd gets extra warmup runs to ensure the daemon JVM is fully JIT-compiled."
  echo "- Gradle uses \`--no-daemon\` to measure honest cold-JVM startup; a warm Gradle daemon"
  echo "  would be closer to mvnd numbers."
} > "$OUT"

ok "markdown report written to $OUT"
section "Done"
