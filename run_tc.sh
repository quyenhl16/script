#!/bin/bash
set -euo pipefail

# ============================================================
# Configuration
# ============================================================

BASE_DIR="/home/vt_admin/anhdt61/check/dist/syssetup-0.1.0-linux-amd64"
REPORT_DIR="$BASE_DIR/reports/xlsx/01HTX"
TESTCASE_DIR="/home/vt_admin/anhdt61/check/dist/testcases"

PROFILE="profiles/01HTX/k8s-connectivity-check.json"


# ============================================================
# Validate arguments
# ============================================================

if [ "$#" -ne 1 ] && [ "$#" -ne 3 ]; then
    echo "Usage: $0 <testcase_name> [ping_count ping_timeout_seconds]"
    exit 1
fi

TC_NAME="$1"
PING_COUNT="${2:-}"
PING_TIMEOUT_SECONDS="${3:-}"

# Only allow safe testcase names
if [[ ! "$TC_NAME" =~ ^[a-zA-Z0-9_-]+$ ]]; then
    echo "ERROR: Invalid testcase name: $TC_NAME"
    echo "Allowed characters: a-z A-Z 0-9 _ -"
    exit 1
fi

if [ -n "$PING_COUNT" ]; then
    if [[ ! "$PING_COUNT" =~ ^[1-9][0-9]*$ ]]; then
        echo "ERROR: ping_count must be a positive integer"
        exit 1
    fi

    if [[ ! "$PING_TIMEOUT_SECONDS" =~ ^[1-9][0-9]*$ ]]; then
        echo "ERROR: ping_timeout_seconds must be a positive integer"
        exit 1
    fi
fi


# ============================================================
# Prepare directories
# ============================================================

DEST_DIR="$TESTCASE_DIR/$TC_NAME"

mkdir -p "$DEST_DIR"
mkdir -p "$REPORT_DIR"


# ============================================================
# Create marker and optional temporary profile
# ============================================================

MARKER=$(mktemp)
TEMP_PROFILE=""
RUN_PROFILE="$PROFILE"

cleanup() {
    rm -f "$MARKER"

    if [ -n "$TEMP_PROFILE" ]; then
        rm -f "$TEMP_PROFILE"
    fi
}

trap cleanup EXIT

if [ -n "$PING_COUNT" ]; then
    if ! command -v jq >/dev/null 2>&1; then
        echo "ERROR: jq is required when overriding ping parameters"
        exit 1
    fi

    TEMP_PROFILE=$(mktemp)

    jq \
        --argjson ping_count "$PING_COUNT" \
        --argjson ping_timeout "$PING_TIMEOUT_SECONDS" \
        '
        if any(.features[]; .id == "k8s-connectivity-check") then
            (.features[]
                | select(.id == "k8s-connectivity-check")
                | .parameters.ping_count) = $ping_count
            |
            (.features[]
                | select(.id == "k8s-connectivity-check")
                | .parameters.ping_timeout_seconds) = $ping_timeout
        else
            error("feature k8s-connectivity-check was not found in the profile")
        end
        ' \
        "$BASE_DIR/$PROFILE" > "$TEMP_PROFILE"

    RUN_PROFILE="$TEMP_PROFILE"
fi


# ============================================================
# Start testcase
# ============================================================

echo
echo "============================================================"
echo "Testcase : $TC_NAME"
echo "Profile  : $PROFILE"
if [ -n "$PING_COUNT" ]; then
    echo "Ping     : count=$PING_COUNT, timeout=${PING_TIMEOUT_SECONDS}s"
else
    echo "Ping     : use profile/default values"
fi
echo "Output   : $DEST_DIR"
echo "============================================================"
echo


cd "$BASE_DIR"


# ============================================================
# Execute syssetup
#
# syssetup may return non-zero when checks fail.
# We must NOT stop here because the Excel report may already
# have been generated.
# ============================================================

set +e

./bin/syssetup run \
    --profile "$RUN_PROFILE" \
    --report-format xlsx

SYSSETUP_RC=$?

set -e


echo
echo "============================================================"

if [ "$SYSSETUP_RC" -eq 0 ]; then
    echo "Syssetup result: PASS"
else
    echo "Syssetup result: FAIL (exit code: $SYSSETUP_RC)"
fi

echo "Searching for generated Excel report..."
echo "============================================================"
echo


# ============================================================
# Move reports generated during this run
# ============================================================

COUNT=0

while IFS= read -r -d '' FILE; do

    FILE_NAME=$(basename "$FILE")

    echo "Found report:"
    echo "  $FILE"

    echo "Moving to:"
    echo "  $DEST_DIR/$FILE_NAME"

    mv -f "$FILE" "$DEST_DIR/"

    COUNT=$((COUNT + 1))

done < <(
    find "$REPORT_DIR" \
        -maxdepth 1 \
        -type f \
        -name "*.xlsx" \
        -newer "$MARKER" \
        -print0
)


# ============================================================
# Validate report
# ============================================================

if [ "$COUNT" -eq 0 ]; then
    echo
    echo "============================================================"
    echo "ERROR: No new Excel report found."
    echo
    echo "Expected report directory:"
    echo "  $REPORT_DIR"
    echo
    echo "Syssetup exit code:"
    echo "  $SYSSETUP_RC"
    echo "============================================================"

    exit 1
fi


# ============================================================
# Final result
# ============================================================

echo
echo "============================================================"
echo "Testcase completed"
echo "============================================================"
echo "Testcase       : $TC_NAME"
echo "Syssetup code  : $SYSSETUP_RC"
echo "Reports moved  : $COUNT"
echo "Saved to       : $DEST_DIR"
echo "============================================================"
echo


# ============================================================
# Preserve syssetup result
#
# 0     -> testcase PASS
# non-0 -> testcase FAIL
#
# Report is moved in both cases.
# ============================================================

exit "$SYSSETUP_RC"
