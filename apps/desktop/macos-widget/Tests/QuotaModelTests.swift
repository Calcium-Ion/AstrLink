import Foundation

/// Plain assertions for the widget's names, clock times and quota choice.
/// Command Line Tools ship no XCTest, so this builds and runs with swiftc
/// (`bun scripts/build-macos-widget.mjs --test`).
@main
struct QuotaModelTests {
  nonisolated(unsafe) static var failures = 0
  nonisolated(unsafe) static var checks = 0

  static func expect<T: Equatable>(
    _ actual: T, _ expected: T, _ what: String, line: Int = #line
  ) {
    checks += 1
    if actual != expected {
      failures += 1
      print("FAIL line \(line): \(what)\n  expected: \(expected)\n  actual:   \(actual)")
    }
  }

  static func main() {
    var calendar = Calendar(identifier: .gregorian)
    calendar.timeZone = TimeZone(identifier: "Asia/Shanghai")!
    // Sunday 2026-11-01 15:20 in Shanghai.
    let now = date("2026-11-01T07:20:00Z")

    func window(
      _ seconds: Int?, used: Double, reset: String? = nil, secondary: Bool = false,
      label: String? = nil
    ) -> QuotaWindow {
      QuotaWindow(
        label: label, limitWindowSeconds: seconds, secondary: secondary, usedPercent: used,
        resetAt: reset.map(date))
    }
    func model(
      _ windows: [[QuotaWindow]], locale: String = "zh-CN", mode: QuotaDisplay = .remaining,
      generated: Date? = nil
    ) -> QuotaPresentation {
      let plans = windows.enumerated().map { index, windows in
        QuotaPlan(
          id: "p\(index)", name: "Plan \(index)", kind: "codex_subscription", windows: windows)
      }
      let snapshot = QuotaSnapshot(
        version: 1, generatedAt: generated ?? now, locale: locale, quotaDisplay: mode,
        plans: plans)
      return QuotaPresentation(snapshot: snapshot, now: now, calendar: calendar)
    }

    // Quotas are named for what they are.
    let names = model([
      [window(18_000, used: 10)], [window(604_800, used: 10)], [window(86_400, used: 10)],
      [window(2_592_000, used: 10)], [window(nil, used: 10, label: "Monthly")],
      [window(nil, used: 10, label: "Droid Core Monthly")],
      [window(604_800, used: 10, label: "Opus")], [window(nil, used: 10, label: "Opus")],
      [window(nil, used: 10)], [window(7_200, used: 10)],
    ]).plans.map(\.primary.name)
    expect(
      names,
      [
        "5 小时额度", "本周额度", "今日额度", "本月额度", "本月额度", "Droid Core 本月额度", "Opus 本周额度",
        "Opus 额度", "滚动额度", "2 小时额度",
      ], "zh-CN quota names")
    expect(
      model([[window(18_000, used: 10)], [window(604_800, used: 10, label: "Opus")]], locale: "en")
        .plans.map(\.primary.name),
      ["5-hour quota", "Opus weekly quota"], "en quota names")
    expect(
      model([[window(18_000, used: 10)], [window(604_800, used: 10)]], locale: "ja")
        .plans.map(\.primary.name),
      ["5 時間の利用枠", "今週の利用枠"], "ja quota names")

    // Resets are clock times: today, tomorrow, a weekday, then a date.
    let zh = model([], locale: "zh-CN")
    expect(zh.resetText(date("2026-11-01T09:18:00Z")).variants, ["17:18 重置"], "same day")
    expect(
      zh.resetText(date("2026-11-02T01:00:00Z")).variants, ["明天 09:00 重置", "明天重置"], "tomorrow")
    expect(
      zh.resetText(date("2026-11-04T01:00:00Z")).variants, ["周三 09:00 重置", "周三重置"], "weekday")
    expect(zh.resetText(date("2026-11-30T01:00:00Z")).variants, ["11月30日重置"], "date")
    expect(zh.resetText(date("2026-11-01T07:00:00Z")).variants, ["15:00 已重置"], "passed today")
    expect(zh.resetText(nil).variants, [], "unknown reset")
    let en = model([], locale: "en")
    expect(en.resetText(date("2026-11-01T09:18:00Z")).variants, ["Resets 17:18"], "en same day")
    expect(
      en.resetText(date("2026-11-04T01:00:00Z")).variants, ["Resets Wed 09:00", "Resets Wed"],
      "en weekday")
    expect(en.resetText(date("2026-11-30T01:00:00Z")).variants, ["Resets Nov 30"], "en date")
    expect(
      model([], locale: "ja").resetText(date("2026-11-01T09:18:00Z")).variants,
      ["17:18 にリセット", "17:18 リセット"], "ja same day")
    let ja = model([], locale: "ja")
    expect(
      ja.resetText(date("2026-11-02T01:00:00Z")).variants, ["明日 09:00 にリセット", "明日にリセット"],
      "ja tomorrow")

    // No countdown or window length ever sits beside a reset.
    for language in ["zh-CN", "en", "ja"] {
      let m = model([], locale: language)
      for hour in stride(from: 0, through: 24 * 40, by: 7) {
        for text in m.resetText(now.addingTimeInterval(Double(hour) * 3600 + 60)).variants {
          for banned in ["后", "後", " in ", "7 天", "7 days", "ago", "前"] {
            expect(text.contains(banned), false, "\(language) reset \"\(text)\" contains \(banned)")
          }
        }
      }
    }

    // The 5-hour window leads, wherever it is listed.
    let lead = model([
      [window(604_800, used: 10, secondary: true), window(18_000, used: 20)],
      [window(86_400, used: 10), window(2_592_000, used: 10, secondary: true)],
      [window(nil, used: 30, label: "gemini-3-pro"), window(nil, used: 40, label: "claude")],
    ])
    expect(lead.plans.map(\.primary.name), ["5 小时额度", "今日额度", "gemini-3-pro 额度"], "primary")
    expect(lead.plans.map { $0.secondary?.name }, ["本周额度", "本月额度", "claude 额度"], "secondary")
    expect(model([[window(18_000, used: 10)]]).plans[0].secondary == nil, true, "single window")

    // A worse window replaces the weekly one on the secondary line.
    let worse = model([
      [
        window(18_000, used: 10), window(604_800, used: 50, secondary: true),
        window(604_800, used: 100, reset: "2026-11-04T01:00:00Z", label: "Opus"),
      ]
    ]).plans[0]
    expect(worse.secondary?.name, "Opus 本周额度", "worst secondary wins")
    expect(
      model([]).secondaryLine(worse.secondary!, short: false).full,
      "Opus 本周额度已用完 · 周三 09:00 重置", "exhausted secondary says when")

    // Tone follows usage; a passed reset brings the quota back.
    let tones = model([
      [window(18_000, used: 79.9)], [window(18_000, used: 80)], [window(18_000, used: 100)],
      [window(18_000, used: 100, reset: "2026-11-01T07:00:00Z")],
    ]).plans.map(\.primary)
    expect(tones.map(\.tone), [.normal, .warning, .critical, .normal], "tones")
    expect(tones.map(\.percent), [20, 20, 0, 100], "remaining percents")
    expect(tones[3].didReset, true, "passed reset")

    // Captions and lines follow the display preference; tone does not.
    let used = model(
      [[window(18_000, used: 88), window(604_800, used: 59, secondary: true)]], mode: .used)
    expect(used.plans[0].primary.percent, 88, "used percent")
    expect(used.plans[0].primary.tone, .warning, "used tone")
    expect(used.caption(used.plans[0].primary), "5 小时额度已用", "used caption")
    expect(
      used.secondaryLine(used.plans[0].secondary!, short: true).full, "本周已用 59%", "used short line")
    let remaining = model([[window(18_000, used: 100), window(604_800, used: 59, secondary: true)]])
    expect(remaining.caption(remaining.plans[0].primary), "5 小时额度已用完", "exhausted caption")
    expect(
      remaining.secondaryLine(remaining.plans[0].secondary!, short: false).full, "本周额度剩 41%", "line"
    )

    // One kicker when the plans agree, a generic one when they do not.
    let same = model([[window(18_000, used: 1)], [window(18_000, used: 2)]])
    expect(same.kicker(same.plans), "5 小时额度剩余", "shared kicker")
    let mixed = model([[window(18_000, used: 1)], [window(86_400, used: 2)]])
    expect(mixed.kicker(mixed.plans), "额度剩余", "generic kicker")
    expect(mixed.kickerIsGeneric(mixed.plans), true, "mixed kicker is generic")
    expect(
      mixed.columnQuotaName(mixed.plans[1].primary).variants, ["今日额度", "今日"],
      "generic column names its quota")

    // Freshness.
    expect(model([], generated: now).updated, "刚刚更新", "just now")
    expect(model([], generated: now.addingTimeInterval(-125)).updated, "更新于 2 分钟前", "minutes")
    expect(model([], generated: now.addingTimeInterval(-70 * 60)).updated, "更新于 14:10", "clock")
    expect(model([], generated: now.addingTimeInterval(-9 * 60)).stale, false, "fresh")
    expect(model([], generated: now.addingTimeInterval(-11 * 60)).stale, true, "stale")
    expect(
      model([], generated: date("2026-10-31T12:00:00Z")).updatedClock, "更新于 昨天 20:00",
      "yesterday")

    // Timeline entries land on resets and the stale mark, in order.
    let snapshot = QuotaSnapshot(
      version: 1, generatedAt: now, locale: "zh-CN", quotaDisplay: .remaining,
      plans: [
        QuotaPlan(
          id: "a", name: "A", kind: "codex_subscription",
          windows: [window(18_000, used: 10, reset: "2026-11-01T09:18:00Z")])
      ])
    let dates = QuotaPresentation.timelineDates(snapshot: snapshot, now: now)
    expect(dates.first, now, "timeline starts now")
    expect(dates.contains(date("2026-11-01T09:18:00Z")), true, "timeline has the reset")
    expect(
      dates.contains(now.addingTimeInterval(QuotaPresentation.staleAfter + 1)), true,
      "timeline has the stale mark")
    expect(dates, dates.sorted(), "timeline sorted")
    expect(dates.count <= 60, true, "timeline bounded")

    // The host's JSON (see macos_widget.rs) decodes; another version does not.
    let fixture = URL(fileURLWithPath: #filePath).deletingLastPathComponent()
      .appendingPathComponent("snapshot-fixture.json")
    let data = try! Data(contentsOf: fixture)
    let decoded = QuotaSnapshot.decode(data)
    expect(decoded?.plans.map(\.id), ["svc_codex", "svc_kimi"], "fixture plans")
    expect(decoded?.plans[0].windows[0].resetAt, date("2026-11-01T09:18:00Z"), "fixture reset")
    expect(decoded?.plans[1].windows[0].label, "Monthly", "fixture label")
    let future = String(data: data, encoding: .utf8)!.replacingOccurrences(
      of: "\"version\": 1", with: "\"version\": 2")
    expect(QuotaSnapshot.decode(Data(future.utf8)) == nil, true, "other versions rejected")

    print("\(checks - failures)/\(checks) checks passed")
    exit(failures == 0 ? 0 : 1)
  }

  static func date(_ text: String) -> Date {
    ISO8601DateFormatter().date(from: text)!
  }
}
