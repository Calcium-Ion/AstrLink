import Foundation

/// The widget's language: the app's setting from the snapshot, so the widget
/// and the app always agree, whatever the system language is.
enum QuotaLanguage {
  case zhCN
  case en
  case ja

  init(tag: String?) {
    let language = tag?.split(whereSeparator: { "-_".contains($0) }).first?.lowercased()
    switch language {
    case "zh": self = .zhCN
    case "ja": self = .ja
    case "en": self = .en
    default:
      // No snapshot yet: follow the system, the same way the host picks its
      // first language.
      self = tag == nil ? QuotaLanguage(tag: Locale.preferredLanguages.first ?? "en") : .en
    }
  }
}

/// How long a quota window is, named for what it is: "5 小时额度",
/// "本周额度". A window length never appears next to a relative time.
enum QuotaPeriod: Equatable {
  case minutes(Int)
  case hours(Int)
  case days(Int)
  case daily
  case weekly
  case monthly
  /// Unknown length, primary or provider-named.
  case rolling
  /// Unknown length, secondary.
  case period

  init(seconds: Int?, secondary: Bool) {
    guard let seconds, seconds > 0 else {
      self = secondary ? .period : .rolling
      return
    }
    switch seconds {
    case 86_400: self = .daily
    case 604_800: self = .weekly
    // Calendar months, as Copilot reports them.
    case 2_419_200...2_678_400: self = .monthly
    case _ where seconds % 86_400 == 0: self = .days(seconds / 86_400)
    case _ where seconds % 3_600 == 0: self = .hours(seconds / 3_600)
    case _ where seconds % 60 == 0: self = .minutes(seconds / 60)
    default: self = secondary ? .period : .rolling
    }
  }

  /// A trailing period word in a provider label ("Monthly", "Droid Core
  /// Monthly") names the period; the rest of the label stays a prefix.
  static func split(label: String) -> (prefix: String, period: QuotaPeriod?) {
    var words = label.split(separator: " ").map(String.init)
    guard let last = words.last else { return (label, nil) }
    let period: QuotaPeriod?
    switch last.lowercased() {
    case "monthly", "month": period = .monthly
    case "weekly", "week": period = .weekly
    case "daily", "day", "today": period = .daily
    default: period = nil
    }
    guard period != nil else { return (label, nil) }
    words.removeLast()
    return (words.joined(separator: " "), period)
  }
}

/// Every string the widget shows, in the three app languages. Copy is plain:
/// it says what a number is and when it resets, nothing more.
struct QuotaCopy {
  let language: QuotaLanguage

  func periodName(_ period: QuotaPeriod) -> String {
    switch language {
    case .zhCN:
      switch period {
      case .minutes(let n): return "\(n) 分钟额度"
      case .hours(let n): return "\(n) 小时额度"
      case .days(let n): return "\(n) 天额度"
      case .daily: return "今日额度"
      case .weekly: return "本周额度"
      case .monthly: return "本月额度"
      case .rolling: return "滚动额度"
      case .period: return "周期额度"
      }
    case .en:
      switch period {
      case .minutes(let n): return "\(n)-minute quota"
      case .hours(let n): return "\(n)-hour quota"
      case .days(let n): return "\(n)-day quota"
      case .daily: return "Daily quota"
      case .weekly: return "Weekly quota"
      case .monthly: return "Monthly quota"
      case .rolling: return "Rolling quota"
      case .period: return "Period quota"
      }
    case .ja:
      switch period {
      case .minutes(let n): return "\(n) 分の利用枠"
      case .hours(let n): return "\(n) 時間の利用枠"
      case .days(let n): return "\(n) 日間の利用枠"
      case .daily: return "今日の利用枠"
      case .weekly: return "今週の利用枠"
      case .monthly: return "今月の利用枠"
      case .rolling: return "ローリング利用枠"
      case .period: return "期間の利用枠"
      }
    }
  }

  /// The period alone, for the narrow medium columns ("本周剩 41%").
  func shortPeriodName(_ period: QuotaPeriod) -> String {
    switch language {
    case .zhCN:
      switch period {
      case .minutes(let n): return "\(n) 分钟"
      case .hours(let n): return "\(n) 小时"
      case .days(let n): return "\(n) 天"
      case .daily: return "今日"
      case .weekly: return "本周"
      case .monthly: return "本月"
      case .rolling: return "滚动"
      case .period: return "周期"
      }
    case .en:
      switch period {
      case .minutes(let n): return "\(n)m"
      case .hours(let n): return "\(n)h"
      case .days(let n): return "\(n)d"
      case .daily: return "Today"
      case .weekly: return "Week"
      case .monthly: return "Month"
      case .rolling: return "Rolling"
      case .period: return "Period"
      }
    case .ja:
      switch period {
      case .minutes(let n): return "\(n) 分"
      case .hours(let n): return "\(n) 時間"
      case .days(let n): return "\(n) 日間"
      case .daily: return "今日"
      case .weekly: return "今週"
      case .monthly: return "今月"
      case .rolling: return "ローリング"
      case .period: return "期間"
      }
    }
  }

  /// A provider-named window: "Opus 本周额度", or "Opus 额度" when its
  /// length is unknown.
  func labeledName(_ label: String, period: QuotaPeriod?) -> String {
    guard let period else {
      switch language {
      case .zhCN: return "\(label) 额度"
      case .en: return "\(label) quota"
      case .ja: return "\(label) の利用枠"
      }
    }
    let name = periodName(period)
    if language == .en, let first = name.first, first.isUppercase {
      return "\(label) \(first.lowercased())\(name.dropFirst())"
    }
    return "\(label) \(name)"
  }

  func remaining(_ name: String) -> String {
    switch language {
    case .zhCN: return "\(name)剩余"
    case .en: return "\(name) left"
    case .ja: return "\(name) 残り"
    }
  }

  func used(_ name: String) -> String {
    switch language {
    case .zhCN: return "\(name)已用"
    case .en: return "\(name) used"
    case .ja: return "\(name) 使用済み"
    }
  }

  func exhausted(_ name: String) -> String {
    switch language {
    case .zhCN: return "\(name)已用完"
    case .en: return "\(name) used up"
    case .ja: return "\(name) 使い切り"
    }
  }

  /// One quota as a line of text: "本周额度剩 41%".
  func line(_ name: String, percent: Int, mode: QuotaDisplay) -> String {
    switch (language, mode) {
    case (.zhCN, .remaining): return "\(name)剩 \(percent)%"
    case (.zhCN, .used): return "\(name)已用 \(percent)%"
    case (.en, .remaining): return "\(name) \(percent)% left"
    case (.en, .used): return "\(name) \(percent)% used"
    case (.ja, .remaining): return "\(name) 残り \(percent)%"
    case (.ja, .used): return "\(name) \(percent)% 使用済み"
    }
  }

  /// The kicker when the visible plans lead with different quotas.
  func genericKicker(_ mode: QuotaDisplay) -> String {
    switch (language, mode) {
    case (.zhCN, .remaining): return "额度剩余"
    case (.zhCN, .used): return "额度已用"
    case (.en, .remaining): return "Quota left"
    case (.en, .used): return "Quota used"
    case (.ja, .remaining): return "利用枠 残り"
    case (.ja, .used): return "利用枠 使用済み"
    }
  }

  var title: String {
    switch language {
    case .zhCN: return "订阅额度"
    case .en: return "Subscription quota"
    case .ja: return "サブスクリプション利用枠"
    }
  }

  var separator: String { " · " }

  // MARK: Clock times

  func weekday(_ index: Int) -> String {
    let names: [String]
    switch language {
    case .zhCN: names = ["周日", "周一", "周二", "周三", "周四", "周五", "周六"]
    case .en: names = ["Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"]
    case .ja: names = ["日曜", "月曜", "火曜", "水曜", "木曜", "金曜", "土曜"]
    }
    return names[(index - 1 + 7) % 7]
  }

  func monthDay(month: Int, day: Int) -> String {
    switch language {
    case .zhCN, .ja: return "\(month)月\(day)日"
    case .en:
      let months = [
        "Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec",
      ]
      return "\(months[(month - 1) % 12]) \(day)"
    }
  }

  func resetToday(_ clock: String) -> String {
    switch language {
    case .zhCN: return "\(clock) 重置"
    case .en: return "Resets \(clock)"
    case .ja: return "\(clock) にリセット"
    }
  }

  /// Japanese drops the particle where a medium column is too narrow.
  func resetTodayCompact(_ clock: String) -> String {
    language == .ja ? "\(clock) リセット" : resetToday(clock)
  }

  func resetTomorrow(_ clock: String?) -> String {
    switch (language, clock) {
    case (.zhCN, let clock?): return "明天 \(clock) 重置"
    case (.zhCN, nil): return "明天重置"
    case (.en, let clock?): return "Resets tomorrow \(clock)"
    case (.en, nil): return "Resets tomorrow"
    case (.ja, let clock?): return "明日 \(clock) にリセット"
    case (.ja, nil): return "明日にリセット"
    }
  }

  func resetWeekday(_ weekday: String, _ clock: String?) -> String {
    switch (language, clock) {
    case (.zhCN, let clock?): return "\(weekday) \(clock) 重置"
    case (.zhCN, nil): return "\(weekday)重置"
    case (.en, let clock?): return "Resets \(weekday) \(clock)"
    case (.en, nil): return "Resets \(weekday)"
    case (.ja, let clock?): return "\(weekday) \(clock) にリセット"
    case (.ja, nil): return "\(weekday)にリセット"
    }
  }

  func resetDate(_ date: String) -> String {
    switch language {
    case .zhCN: return "\(date)重置"
    case .en: return "Resets \(date)"
    case .ja: return "\(date)にリセット"
    }
  }

  /// The window's reset time has passed since the host last read it.
  func didReset(_ clock: String?) -> String {
    switch (language, clock) {
    case (.zhCN, let clock?): return "\(clock) 已重置"
    case (.zhCN, nil): return "已重置"
    case (.en, let clock?): return "Reset at \(clock)"
    case (.en, nil): return "Reset"
    case (.ja, let clock?): return "\(clock) にリセット済み"
    case (.ja, nil): return "リセット済み"
    }
  }

  // MARK: Freshness

  var updatedJustNow: String {
    switch language {
    case .zhCN: return "刚刚更新"
    case .en: return "Updated just now"
    case .ja: return "たった今更新"
    }
  }

  func updatedMinutesAgo(_ minutes: Int) -> String {
    switch language {
    case .zhCN: return "更新于 \(minutes) 分钟前"
    case .en: return "Updated \(minutes) min ago"
    case .ja: return "\(minutes) 分前に更新"
    }
  }

  func updatedAt(_ time: String) -> String {
    switch language {
    case .zhCN: return "更新于 \(time)"
    case .en: return "Updated \(time)"
    case .ja: return "\(time) に更新"
    }
  }

  func yesterday(_ clock: String) -> String {
    switch language {
    case .zhCN: return "昨天 \(clock)"
    case .en: return "yesterday \(clock)"
    case .ja: return "昨日 \(clock)"
    }
  }

  // MARK: Other states

  func morePlans(_ count: Int) -> String {
    switch language {
    case .zhCN: return "另有 \(count) 个套餐"
    case .en: return count == 1 ? "1 more plan" : "\(count) more plans"
    case .ja: return "ほか \(count) 件のプラン"
    }
  }

  var noPlans: String {
    switch language {
    case .zhCN: return "没有订阅套餐"
    case .en: return "No subscription plans"
    case .ja: return "サブスクリプションプランがありません"
    }
  }

  var noData: String {
    switch language {
    case .zhCN: return "打开 AstrLink 读取额度"
    case .en: return "Open AstrLink to read quotas"
    case .ja: return "AstrLink を開くと利用枠を表示します"
    }
  }

  var unavailable: String {
    switch language {
    case .zhCN: return "暂时读不到额度"
    case .en: return "Quota unavailable"
    case .ja: return "利用枠を読み取れません"
    }
  }
}
