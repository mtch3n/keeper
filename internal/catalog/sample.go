package catalog

// sampleTokenThreshold is SPEC R5.3a and R5.3b's shared bulk-accept
// threshold: at or above this share of non-null samples, catalog init
// proposes token instead of scan. Below it and above zero, it proposes
// scan and reports the rate — raising `notes` (measured 10%) or `memo`
// (measured 2%) to token would mask whole rows where span redaction already
// handles the hits.
const sampleTokenThreshold = 0.90
