(function (root, factory) {
  const api = factory();
  if (typeof module === "object" && module.exports) module.exports = api;
  else root.CleanupPreview = api;
})(typeof globalThis !== "undefined" ? globalThis : this, function () {
  "use strict";

  const bytes = (value) => {
    const number = Number(value);
    return Number.isFinite(number) && number > 0 ? Math.floor(number) : 0;
  };

  function build(records, disk) {
    const files = Array.isArray(records) ? records : [];
    const categories = new Map();
    const advice = {safe: {count: 0, bytes: 0}, review: {count: 0, bytes: 0}, danger: {count: 0, bytes: 0}};
    let reclaimableBytes = 0;

    for (const record of files) {
      const size = bytes(record && record.size);
      reclaimableBytes += size;
      const category = record?.advice?.category || "其他";
      const categorySummary = categories.get(category) || {category, count: 0, bytes: 0};
      categorySummary.count++;
      categorySummary.bytes += size;
      categories.set(category, categorySummary);

      const level = record?.advice?.level;
      const adviceSummary = advice[level] || advice.review;
      adviceSummary.count++;
      adviceSummary.bytes += size;
    }

    const totalBytes = bytes(disk && disk.total);
    const freeBefore = Math.min(totalBytes || Number.MAX_SAFE_INTEGER, bytes(disk && disk.free));
    const freeAfter = totalBytes ? Math.min(totalBytes, freeBefore + reclaimableBytes) : 0;
    const usedBefore = totalBytes ? Math.max(0, totalBytes - freeBefore) : 0;
    const usedAfter = totalBytes ? Math.max(0, totalBytes - freeAfter) : 0;

    return {
      fileCount: files.length,
      reclaimableBytes,
      disk: {
        path: disk?.path || "",
        totalBytes,
        freeBefore,
        freeAfter,
        usedBefore,
        usedAfter,
        usedPercentBefore: totalBytes ? usedBefore / totalBytes * 100 : 0,
        usedPercentAfter: totalBytes ? usedAfter / totalBytes * 100 : 0
      },
      advice,
      categories: [...categories.values()].sort((a, b) => b.bytes - a.bytes || a.category.localeCompare(b.category)),
      largest: files
        .map((record) => ({path: record.path || "", size: bytes(record.size), category: record?.advice?.category || "其他"}))
        .sort((a, b) => b.size - a.size || a.path.localeCompare(b.path))
        .slice(0, 5)
    };
  }

  return {build};
});
