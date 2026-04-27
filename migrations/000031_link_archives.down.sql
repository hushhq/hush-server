-- Migration 000031 (down): drop link archive tables.

DROP TABLE IF EXISTS link_archive_chunks;
DROP TABLE IF EXISTS link_archives;
