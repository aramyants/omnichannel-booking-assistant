#!/usr/bin/env bash
set -euo pipefail

# Provision before deploying SMS delivery reconciliation. This does not change
# runtime access, send messages, or modify customer documents.
project="${GCP_PROJECT_ID:?Set GCP_PROJECT_ID}"
indexes="$(gcloud firestore indexes composite list --project="$project" \
  --database='(default)' --format=json)"
if python3 -c '
import json,sys
expected=[("kind","ASCENDING"),("outcome","ASCENDING"),("updated_at","ASCENDING")]
for index in json.load(sys.stdin):
    fields=[(f.get("fieldPath"),f.get("order")) for f in index.get("fields",[]) if f.get("fieldPath")!="__name__"]
    if "/collectionGroups/native_booking_notifications/" in index.get("name","") and index.get("queryScope")=="COLLECTION" and fields==expected:
        print("SMS delivery index:", index.get("state"))
        sys.exit(0)
sys.exit(1)
' <<< "$indexes"; then
  exit 0
fi
gcloud firestore indexes composite create --project="$project" \
  --database='(default)' --collection-group=native_booking_notifications \
  --query-scope=COLLECTION \
  --field-config=field-path=kind,order=ascending \
  --field-config=field-path=outcome,order=ascending \
  --field-config=field-path=updated_at,order=ascending --quiet
