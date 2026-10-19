CREATE TABLE verified_reviews (
 record_id TEXT PRIMARY KEY,
 phone_hash TEXT NOT NULL,
 staff_id TEXT NOT NULL,
 staff_name TEXT NOT NULL,
 rating INTEGER NOT NULL CHECK(rating BETWEEN 1 AND 5),
 comment TEXT NOT NULL DEFAULT '',
 display_name TEXT NOT NULL DEFAULT '',
 published INTEGER NOT NULL DEFAULT 0 CHECK(published IN (0,1)),
 locale TEXT NOT NULL CHECK(locale IN ('ru','en','hy')),
 created_at INTEGER NOT NULL
);
CREATE INDEX verified_reviews_staff ON verified_reviews(staff_id,published,created_at DESC);
