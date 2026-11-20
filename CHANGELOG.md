# Changelog

## 0.3.0 - 2026-11-20

### Changed

- Reject file names that are not a single plain name, rather than shortening them
- Report a file already at the destination whose size differs from the shared one

### Fixed

- Stop a download overwriting an existing .part file
- Report two files with the same name instead of skipping the second
- Download the rest of a folder when one file's details cannot be read
- Accept file sizes and chunk counts written as decimals or text

## 0.2.0 - 2026-11-20

### Added

- Accept share links in every shape Filen's apps write

### Fixed

- Report a malformed share link without its key, before contacting Filen

## 0.1.2 - 2026-11-20

### Updated

- Build release binaries with Go 1.27.4

## 0.1.1 - 2026-10-23

### Updated

- Build release binaries with the newest Go 1.27 patch release

## 0.1.0 - 2026-09-23

### Added

- Initial release