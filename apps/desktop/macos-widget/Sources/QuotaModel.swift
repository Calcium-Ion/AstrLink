import Foundation

/// Follows `usageWindowTone` in the app: the limit reached is critical, 80%
/// used warns. Tone always follows usage, whichever way percentages read.
enum QuotaTone: Int, Comparable {
  case normal = 0
  case warning
  case critical

  init(usedPercent: Double) {
    if usedPercent >= 100 {
      self = .critical
    } else if usedPercent >= 80 {
      self = .warning
    } else {
      self = .normal
    }
  }

  static func < (lhs: QuotaTone, rhs: QuotaTone) -> Bool { lhs.rawValue < rhs.rawValue }
}

/// Text that has shorter fallbacks; views show the first one that fits.
struct FittingText: Equatable {
  var variants: [String]

  init(_ variants: [String]) {
    var unique: [String] = []
    for variant in variants where !variant.isEmpty && !unique.contains(variant) {
      unique.append(variant)
    }
    self.variants = unique
  }

  var full: String { variants.first ?? "" }
  var isEmpty: Bool { variants.isEmpty }
}

/// One quota window, ready to draw.
struct WindowPresentation: Equatable {
  var name: String
  var shortName: String
  var percent: Int
  /// How much of the bar to fill, 0...1.
  var fill: Double
  var tone: QuotaTone
  /// The reset clock time, long and short forms; empty when unknown.
  var reset: FittingText
  /// The reset time passed after the host's last read, so the quota is back.
  var didReset: Bool

  var exhausted: Bool { tone == .critical }
}

struct PlanPresentation: Equatable, Identifiable {
  var id: String
  var name: String
  var kind: String
  /// The window the big number shows: the 5-hour quota when there is one.
  var primary: WindowPresentation
  /// The weekly (or longest) window, unless another one is in a worse state.
  var secondary: WindowPresentation?
}

/// Everything a widget view needs for one timeline entry.
struct QuotaPresentation {
  static let staleAfter: TimeInterval = 10 * 60

  var copy: QuotaCopy
  var mode: QuotaDisplay
  var plans: [PlanPresentation]
  var now: Date
  var generatedAt: Date?
  var calendar: Calendar

  init(snapshot: QuotaSnapshot?, now: Date, calendar: Calendar = .current) {
    copy = QuotaCopy(language: QuotaLanguage(tag: snapshot?.locale))
    mode = snapshot?.quotaDisplay ?? .remaining
    self.now = now
    self.calendar = calendar
    generatedAt = snapshot?.generatedAt
    plans = []
    plans = snapshot?.plans.compactMap { plan(from: $0) } ?? []
  }

  var hasData: Bool { generatedAt != nil }

  /// The host has not refreshed the numbers for a while (stopped, offline or
  /// failing); the widget keeps them and says when they were read.
  var stale: Bool {
    guard let generatedAt else { return false }
    return now.timeIntervalSince(generatedAt) > Self.staleAfter
  }

  // MARK: Building

  private func plan(from plan: QuotaPlan) -> PlanPresentation? {
    guard let primaryIndex = Self.primaryIndex(plan.windows) else { return nil }
    let primary = window(from: plan.windows[primaryIndex])
    var others = plan.windows
    others.remove(at: primaryIndex)
    let secondary = Self.secondary(others.map { (window: $0, view: window(from: $0)) })
    return PlanPresentation(
      id: plan.id, name: plan.name, kind: plan.kind, primary: primary, secondary: secondary)
  }

  /// The 5-hour window, else the shortest window that is not a secondary one.
  static func primaryIndex(_ windows: [QuotaWindow]) -> Int? {
    if let index = windows.firstIndex(where: { $0.limitWindowSeconds == 18_000 && $0.label == nil })
    {
      return index
    }
    if let index = windows.firstIndex(where: { $0.limitWindowSeconds == 18_000 }) {
      return index
    }
    let candidates = windows.indices.filter { !windows[$0].secondary }
    let pool = candidates.isEmpty ? Array(windows.indices) : candidates
    return pool.min { lhs, rhs in
      (windows[lhs].limitWindowSeconds ?? .max) < (windows[rhs].limitWindowSeconds ?? .max)
    }
  }

  /// The worst state among the other windows; on a tie the unnamed weekly
  /// window, then the longest one.
  private static func secondary(
    _ others: [(window: QuotaWindow, view: WindowPresentation)]
  ) -> WindowPresentation? {
    func rank(_ window: QuotaWindow) -> (Int, Int) {
      let weekly = window.limitWindowSeconds == 604_800 && window.label == nil ? 1 : 0
      let unnamed = window.label == nil ? 1 : 0
      return (weekly * 2 + unnamed, window.limitWindowSeconds ?? 0)
    }
    return others.max { lhs, rhs in
      if lhs.view.tone != rhs.view.tone { return lhs.view.tone < rhs.view.tone }
      return rank(lhs.window) < rank(rhs.window)
    }?.view
  }

  private func window(from window: QuotaWindow) -> WindowPresentation {
    let name: String
    let shortName: String
    if let label = window.label?.trimmingCharacters(in: .whitespaces), !label.isEmpty {
      let split = QuotaPeriod.split(label: label)
      let period =
        split.period
        ?? (window.limitWindowSeconds == nil
          ? nil : QuotaPeriod(seconds: window.limitWindowSeconds, secondary: window.secondary))
      if split.prefix.isEmpty, let period {
        name = copy.periodName(period)
        shortName = copy.shortPeriodName(period)
      } else {
        name = copy.labeledName(split.prefix, period: period)
        shortName = split.prefix
      }
    } else {
      let period = QuotaPeriod(seconds: window.limitWindowSeconds, secondary: window.secondary)
      name = copy.periodName(period)
      shortName = copy.shortPeriodName(period)
    }

    let didReset = window.resetAt.map { $0 <= now } ?? false
    let raw = window.usedPercent.isFinite ? min(max(window.usedPercent, 0), 100) : 0
    let used = didReset ? 0 : raw
    let shown = mode == .remaining ? 100 - used : used
    let percent = Int(shown.rounded())
    return WindowPresentation(
      name: name,
      shortName: shortName,
      percent: percent,
      fill: shown <= 0 ? 0 : max(shown / 100, 0.02),
      tone: QuotaTone(usedPercent: didReset ? 0 : window.usedPercent),
      reset: resetText(window.resetAt),
      didReset: didReset)
  }

  // MARK: Clock times

  func clock(_ date: Date) -> String {
    let parts = calendar.dateComponents([.hour, .minute], from: date)
    return String(format: "%02d:%02d", parts.hour ?? 0, parts.minute ?? 0)
  }

  private func dayOffset(_ date: Date) -> Int {
    calendar.dateComponents(
      [.day], from: calendar.startOfDay(for: now), to: calendar.startOfDay(for: date)
    ).day ?? 0
  }

  /// Always a clock time, never a countdown: "17:18 重置", "明天 09:00 重置",
  /// "周三 09:00 重置" (short "周三重置"), then a date.
  func resetText(_ date: Date?) -> FittingText {
    guard let date else { return FittingText([]) }
    let time = clock(date)
    if date <= now {
      return FittingText([copy.didReset(dayOffset(date) == 0 ? time : nil)])
    }
    switch dayOffset(date) {
    case 0:
      return FittingText([copy.resetToday(time), copy.resetTodayCompact(time)])
    case 1:
      return FittingText([copy.resetTomorrow(time), copy.resetTomorrow(nil)])
    case 2...6:
      let weekday = copy.weekday(calendar.component(.weekday, from: date))
      return FittingText([copy.resetWeekday(weekday, time), copy.resetWeekday(weekday, nil)])
    default:
      let parts = calendar.dateComponents([.month, .day], from: date)
      let day = copy.monthDay(month: parts.month ?? 1, day: parts.day ?? 1)
      // Japanese cannot fit "…にリセット" in a medium column; the bare date
      // is its last resort. The others shrink a little instead.
      return FittingText([copy.resetDate(day)] + (copy.language == .ja ? [day] : []))
    }
  }

  /// A past time as a clock time: "14:05", "昨天 14:05", then a date.
  func pastClock(_ date: Date) -> String {
    switch dayOffset(date) {
    case 0: return clock(date)
    case -1: return copy.yesterday(clock(date))
    default:
      let parts = calendar.dateComponents([.month, .day], from: date)
      return copy.monthDay(month: parts.month ?? 1, day: parts.day ?? 1)
    }
  }

  /// The large widget's footer: minutes for the first hour, then a clock time.
  var updated: String {
    guard let generatedAt else { return "" }
    let minutes = Int(now.timeIntervalSince(generatedAt) / 60)
    if minutes < 1 { return copy.updatedJustNow }
    if minutes < 60 { return copy.updatedMinutesAgo(minutes) }
    return copy.updatedAt(pastClock(generatedAt))
  }

  /// The stale mark on the small and medium widgets.
  var updatedClock: String {
    guard let generatedAt else { return "" }
    return copy.updatedAt(pastClock(generatedAt))
  }

  // MARK: Text for each place

  /// The caption above the small widget's big number.
  func caption(_ window: WindowPresentation) -> String {
    if window.exhausted { return copy.exhausted(window.name) }
    return mode == .remaining ? copy.remaining(window.name) : copy.used(window.name)
  }

  /// One header for the medium and large widgets: the shared quota's name,
  /// or a generic one when the visible plans lead with different quotas.
  func kicker(_ plans: [PlanPresentation]) -> String {
    let names = Set(plans.map(\.primary.name))
    guard names.count == 1, let name = names.first else { return copy.genericKicker(mode) }
    return mode == .remaining ? copy.remaining(name) : copy.used(name)
  }

  func kickerIsGeneric(_ plans: [PlanPresentation]) -> Bool {
    Set(plans.map(\.primary.name)).count > 1
  }

  /// The secondary quota as one line. Exhausted, it says so and when it
  /// resets; the big number above stays the primary quota.
  func secondaryLine(_ window: WindowPresentation, short: Bool) -> FittingText {
    let name = short ? window.shortName : window.name
    if window.exhausted {
      let base = copy.exhausted(name)
      return FittingText(
        window.reset.variants.map { base + copy.separator + $0 } + [base])
    }
    let line = copy.line(name, percent: window.percent, mode: mode)
    if short {
      // English runs long in a medium column; drop the verb before the number.
      return FittingText([line, "\(name) \(window.percent)%"])
    }
    return FittingText([line])
  }

  /// A medium column's own quota name, when the kicker cannot name it.
  func columnQuotaName(_ window: WindowPresentation) -> FittingText {
    FittingText([window.name, window.shortName])
  }

  /// The large row's caption: "5 小时额度 · 17:18 重置".
  func largeCaption(_ window: WindowPresentation) -> FittingText {
    let base = window.exhausted ? copy.exhausted(window.name) : window.name
    return FittingText(window.reset.variants.map { base + copy.separator + $0 } + [base])
  }

  // MARK: Timeline

  /// Moments when the text changes on its own: each reset time (the quota is
  /// back), each minute of the footer's first ten minutes, the stale mark, and
  /// then coarser footer steps. No reload is spent on any of them.
  static func timelineDates(snapshot: QuotaSnapshot?, now: Date, limit: Int = 60) -> [Date] {
    var dates: Set<Date> = [now]
    let horizon = now.addingTimeInterval(24 * 3600)
    if let snapshot {
      for window in snapshot.plans.flatMap(\.windows) {
        if let reset = window.resetAt, reset > now, reset <= horizon {
          dates.insert(reset)
        }
      }
      let generated = snapshot.generatedAt
      var steps: [TimeInterval] = (1...10).map { Double($0) * 60 }
      steps.append(staleAfter + 1)
      steps += stride(from: 15.0, through: 60, by: 5).map { $0 * 60 }
      steps += stride(from: 2.0, through: 24, by: 1).map { $0 * 3600 }
      for step in steps {
        let date = generated.addingTimeInterval(step)
        if date > now, date <= horizon { dates.insert(date) }
      }
    }
    return Array(dates.sorted().prefix(limit))
  }
}
