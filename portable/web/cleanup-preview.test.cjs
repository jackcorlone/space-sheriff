const test = require("node:test");
const assert = require("node:assert/strict");
const {build} = require("./cleanup-preview.js");

test("builds a cleanup preview without changing source records", () => {
  const records = [
    {path: "/tmp/cache.bin", size: 300, advice: {level: "safe", category: "缓存与日志"}},
    {path: "/Users/me/movie.mp4", size: 200, advice: {level: "review", category: "个人文件"}}
  ];
  const before = structuredClone(records);
  const preview = build(records, {path: "/", total: 1000, free: 100});

  assert.deepEqual(records, before);
  assert.equal(preview.fileCount, 2);
  assert.equal(preview.reclaimableBytes, 500);
  assert.equal(preview.disk.freeBefore, 100);
  assert.equal(preview.disk.freeAfter, 600);
  assert.equal(preview.disk.usedPercentAfter, 40);
  assert.deepEqual(preview.advice.safe, {count: 1, bytes: 300});
  assert.deepEqual(preview.categories.map((item) => item.category), ["缓存与日志", "个人文件"]);
});

test("caps projected free space and normalizes malformed sizes", () => {
  const preview = build([
    {path: "large", size: 900, advice: {level: "safe"}},
    {path: "invalid", size: -10, advice: {level: "unknown"}}
  ], {path: "C:\\\\", total: 1000, free: 400});

  assert.equal(preview.reclaimableBytes, 900);
  assert.equal(preview.disk.freeAfter, 1000);
  assert.equal(preview.disk.usedAfter, 0);
  assert.equal(preview.advice.review.count, 1);
  assert.equal(preview.categories[0].category, "其他");
});

test("works when disk capacity is unavailable", () => {
  const preview = build([{path: "one", size: 42, advice: {level: "review", category: "其他"}}]);
  assert.equal(preview.reclaimableBytes, 42);
  assert.equal(preview.disk.totalBytes, 0);
  assert.equal(preview.disk.freeAfter, 0);
});
