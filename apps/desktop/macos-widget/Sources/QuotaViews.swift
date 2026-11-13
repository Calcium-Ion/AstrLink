import SwiftUI
import WidgetKit

struct QuotaEntry: TimelineEntry {
  var date: Date
  var snapshot: QuotaSnapshot?
  /// The small widget's configured plan; nil for the first plan.
  var planID: String?
}

/// Picks the layout for the widget's size. Sizes come from WidgetKit; only
/// the paddings and type sizes follow the approved mockup.
struct QuotaWidgetView: View {
  var entry: QuotaEntry
  @Environment(\.widgetFamily) private var systemFamily
  @Environment(\.quotaFamilyOverride) private var familyOverride
  @Environment(\.colorScheme) private var scheme
  @Environment(\.widgetRenderingMode) private var systemRendering
  @Environment(\.quotaRenderingOverride) private var renderingOverride

  var body: some View {
    let palette = QuotaPalette(
      scheme: scheme, rendering: renderingOverride ?? QuotaRendering(systemRendering))
    let model = QuotaPresentation(snapshot: entry.snapshot, now: entry.date)
    content(model: model, palette: palette)
      .foregroundStyle(palette.foreground)
      .containerBackground(for: .widget) { palette.card }
  }

  @ViewBuilder
  private func content(model: QuotaPresentation, palette: QuotaPalette) -> some View {
    switch familyOverride ?? systemFamily {
    case .systemSmall:
      if let plan = model.plans.first(where: { $0.id == entry.planID })
        ?? (entry.planID == nil ? model.plans.first : nil)
      {
        SmallQuotaView(model: model, plan: plan, palette: palette)
      } else {
        QuotaMessageView(model: model, palette: palette, compact: true, message: message(model))
      }
    case .systemLarge:
      if model.plans.isEmpty {
        QuotaMessageView(model: model, palette: palette, compact: false, message: message(model))
      } else {
        LargeQuotaView(model: model, palette: palette)
      }
    default:
      if model.plans.isEmpty {
        QuotaMessageView(model: model, palette: palette, compact: false, message: message(model))
      } else {
        MediumQuotaView(model: model, palette: palette)
      }
    }
  }

  private func message(_ model: QuotaPresentation) -> String {
    if !model.hasData { return model.copy.noData }
    if model.plans.isEmpty { return model.copy.noPlans }
    // The configured plan is gone from the host's latest read.
    return model.copy.unavailable
  }
}

// MARK: - Sizes

struct SmallQuotaView: View {
  var model: QuotaPresentation
  var plan: PlanPresentation
  var palette: QuotaPalette

  var body: some View {
    let window = plan.primary
    VStack(alignment: .leading, spacing: 0) {
      HStack(spacing: 7) {
        LogoTile(kind: plan.kind, palette: palette)
        Text(plan.name)
          .font(.system(size: 13, weight: .semibold))
          .lineLimit(1)
          .minimumScaleFactor(0.85)
      }
      .frame(height: 22)

      Text(model.caption(window))
        .font(.system(size: 11))
        .foregroundStyle(palette.muted)
        .lineLimit(1)
        .lineBox(16)
        .padding(.top, 8)

      HStack(spacing: 4) {
        if window.exhausted { AlertIcon(size: 18, palette: palette) }
        HStack(alignment: .lastTextBaseline, spacing: 0) {
          Text("\(window.percent)")
            .font(.system(size: 30, weight: .semibold))
            .tracking(-0.75)
          Text("%")
            .font(.system(size: 17, weight: .medium))
        }
        .monospacedDigit()
        .foregroundStyle(palette.number(window.tone))
      }
      .frame(height: 34)

      QuotaBar(window: window, palette: palette, height: 6)
        .padding(.top, 5)

      HStack(spacing: 6) {
        FitText(window.reset)
        if model.stale {
          Spacer(minLength: 0)
          Text(model.updatedClock).lineLimit(1).layoutPriority(1)
        }
      }
      .font(.system(size: 11))
      .monospacedDigit()
      .foregroundStyle(palette.muted)
      .lineBox(15)
      .padding(.top, 5)

      Spacer(minLength: 0)

      if let secondary = plan.secondary {
        Rectangle().fill(palette.line).frame(height: 1)
        SecondaryLine(model: model, window: secondary, palette: palette, short: false)
          .padding(.top, 6)
      }
    }
    .padding(EdgeInsets(top: 13, leading: 14, bottom: 12, trailing: 14))
    .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading)
  }
}

struct MediumQuotaView: View {
  var model: QuotaPresentation
  var palette: QuotaPalette

  var body: some View {
    let plans = Array(model.plans.prefix(4))
    let generic = model.kickerIsGeneric(plans)
    VStack(alignment: .leading, spacing: 0) {
      HStack(spacing: 8) {
        Kicker(text: model.kicker(plans), palette: palette)
        if model.stale {
          Spacer(minLength: 0)
          Text(model.updatedClock)
            .font(.system(size: 11))
            .foregroundStyle(palette.muted)
            .lineLimit(1)
        }
      }
      .lineBox(16)

      Spacer(minLength: 0)

      HStack(alignment: .top, spacing: 14) {
        ForEach(plans) { plan in
          MediumColumn(model: model, plan: plan, palette: palette, generic: generic)
            .frame(maxWidth: .infinity, alignment: .leading)
        }
      }
    }
    .padding(EdgeInsets(top: 12, leading: 15, bottom: 12, trailing: 15))
    .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading)
  }
}

private struct MediumColumn: View {
  var model: QuotaPresentation
  var plan: PlanPresentation
  var palette: QuotaPalette
  var generic: Bool

  var body: some View {
    let window = plan.primary
    VStack(alignment: .leading, spacing: 0) {
      HStack(spacing: 6) {
        LogoView(kind: plan.kind, size: 16, palette: palette)
        PlanName(name: plan.name, others: model.plans.map(\.name))
          .font(.system(size: 12, weight: .medium))
      }
      .frame(height: 20)

      HStack(spacing: 3) {
        if window.exhausted { AlertIcon(size: 14, palette: palette) }
        Text("\(window.percent)%")
          .font(.system(size: 22, weight: .semibold))
          .tracking(-0.44)
          .monospacedDigit()
          .foregroundStyle(palette.number(window.tone))
          .lineLimit(1)
          .minimumScaleFactor(0.8)
      }
      .frame(height: 28)
      .padding(.top, generic ? 2 : 4)

      QuotaBar(window: window, palette: palette, height: 5)
        .padding(.top, 4)

      Group {
        // Plans lead with different quotas, so each column names its own.
        if generic {
          FitText(model.columnQuotaName(window))
            .lineBox(15)
            .padding(.top, 4)
        }
        FitText(window.reset)
          .lineBox(15)
          .padding(.top, generic ? 0 : 6)
      }
      .font(.system(size: 11))
      .monospacedDigit()
      .foregroundStyle(palette.muted)

      if let secondary = plan.secondary {
        SecondaryLine(model: model, window: secondary, palette: palette, short: true)
      } else {
        Color.clear.frame(height: 15)
      }
    }
  }
}

struct LargeQuotaView: View {
  var model: QuotaPresentation
  var palette: QuotaPalette

  var body: some View {
    let plans = Array(model.plans.prefix(4))
    VStack(alignment: .leading, spacing: 0) {
      HStack(spacing: 8) {
        Kicker(text: model.copy.title, palette: palette)
        Spacer(minLength: 0)
        Kicker(text: model.kicker(plans), palette: palette)
      }
      .lineBox(16)

      VStack(spacing: 13) {
        ForEach(plans) { plan in
          LargeRow(model: model, plan: plan, palette: palette)
        }
      }
      .padding(.top, 12)

      Spacer(minLength: 0)

      HStack(spacing: 8) {
        Text(model.updated).lineLimit(1)
        if model.plans.count > plans.count {
          Spacer(minLength: 0)
          Text(model.copy.morePlans(model.plans.count - plans.count)).lineLimit(1)
        }
      }
      .font(.system(size: 11))
      .foregroundStyle(palette.muted)
      .lineBox(16)
    }
    .padding(EdgeInsets(top: 14, leading: 16, bottom: 14, trailing: 16))
    .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading)
  }
}

private struct LargeRow: View {
  var model: QuotaPresentation
  var plan: PlanPresentation
  var palette: QuotaPalette

  var body: some View {
    let window = plan.primary
    VStack(spacing: 5) {
      HStack(spacing: 6) {
        LogoView(kind: plan.kind, size: 16, palette: palette)
        Text(plan.name)
          .font(.system(size: 12, weight: .medium))
          .lineLimit(1)
        Spacer(minLength: 8)
        HStack(spacing: 3) {
          if window.exhausted { AlertIcon(size: 14, palette: palette) }
          Text("\(window.percent)%")
            .font(.system(size: 20, weight: .semibold))
            .tracking(-0.4)
            .monospacedDigit()
            .foregroundStyle(palette.number(window.tone))
        }
        .layoutPriority(1)
      }
      .frame(height: 24)

      QuotaBar(window: window, palette: palette, height: 7)

      HStack(spacing: 8) {
        FitText(model.largeCaption(window))
          .foregroundStyle(palette.muted)
        Spacer(minLength: 0)
        if let secondary = plan.secondary {
          SecondaryLine(model: model, window: secondary, palette: palette, short: false)
            .layoutPriority(1)
        }
      }
      .font(.system(size: 11))
      .monospacedDigit()
      .lineBox(15)
    }
  }
}

/// No numbers to show: no snapshot yet, no plans, or the chosen plan is gone.
/// A click opens AstrLink, like every other widget click.
struct QuotaMessageView: View {
  var model: QuotaPresentation
  var palette: QuotaPalette
  var compact: Bool
  var message: String

  var body: some View {
    VStack(alignment: .leading, spacing: 0) {
      Kicker(text: model.copy.title, palette: palette).lineBox(16)
      Spacer(minLength: 0)
      Text(message)
        .font(.system(size: compact ? 12 : 13))
        .foregroundStyle(palette.secondary)
        .lineLimit(compact ? 3 : 1)
        .frame(maxWidth: .infinity, alignment: compact ? .leading : .center)
      Spacer(minLength: 0)
      if model.stale {
        Text(model.updatedClock)
          .font(.system(size: 11))
          .foregroundStyle(palette.muted)
          .lineBox(15)
      }
    }
    .padding(
      compact
        ? EdgeInsets(top: 13, leading: 14, bottom: 12, trailing: 14)
        : EdgeInsets(top: 12, leading: 15, bottom: 12, trailing: 15)
    )
    .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading)
  }
}

// MARK: - Parts

private struct Kicker: View {
  var text: String
  var palette: QuotaPalette

  var body: some View {
    Text(text)
      .font(.system(size: 11, weight: .medium))
      .tracking(0.66)
      .foregroundStyle(palette.muted)
      .lineLimit(1)
  }
}

/// The secondary quota: quiet when fine, in its tone with the warning icon
/// when exhausted, so the state still reads with the color drained.
private struct SecondaryLine: View {
  var model: QuotaPresentation
  var window: WindowPresentation
  var palette: QuotaPalette
  var short: Bool

  var body: some View {
    HStack(spacing: 3) {
      if window.exhausted { AlertIcon(size: 11, palette: palette) }
      FitText(model.secondaryLine(window, short: short))
    }
    .font(.system(size: 11))
    .monospacedDigit()
    .foregroundStyle(palette.line(window.tone))
    .lineBox(15)
  }
}

struct AlertIcon: View {
  var size: CGFloat
  var palette: QuotaPalette

  var body: some View {
    Image(systemName: "exclamationmark.triangle")
      .font(.system(size: size * 0.82, weight: .medium))
      .foregroundStyle(palette.danger)
      .frame(width: size, height: size)
      .widgetAccentable()
  }
}

struct QuotaBar: View {
  var window: WindowPresentation
  var palette: QuotaPalette
  var height: CGFloat

  var body: some View {
    GeometryReader { proxy in
      ZStack(alignment: .leading) {
        Capsule().fill(palette.track)
        if window.fill > 0 {
          Capsule()
            .fill(palette.fill(window.tone))
            .frame(width: max(height, proxy.size.width * window.fill))
            .widgetAccentable()
        }
      }
    }
    .frame(height: height)
  }
}

/// The first variant that fits on one line; the last one truncates.
struct FitText: View {
  var text: FittingText

  init(_ text: FittingText) { self.text = text }

  var body: some View {
    ViewThatFits(in: .horizontal) {
      ForEach(Array(text.variants.dropLast().enumerated()), id: \.offset) { _, variant in
        Text(variant).lineLimit(1).fixedSize(horizontal: true, vertical: false)
      }
      Text(text.variants.last ?? "").lineLimit(1).minimumScaleFactor(0.85)
    }
  }
}

/// A medium column is narrow: the full name if it fits, else its first word
/// when no other visible plan shares it ("Claude Code" → "Claude").
private struct PlanName: View {
  var name: String
  var others: [String]

  var body: some View {
    let first = name.split(separator: " ").first.map(String.init) ?? name
    let firstIsUnique =
      first != name && first.count >= 3
      && others.filter { $0.split(separator: " ").first.map(String.init) == first }.count == 1
    FitText(FittingText(firstIsUnique ? [name, first] : [name]))
  }
}

extension View {
  /// A fixed line box, like the mockup's CSS line heights.
  func lineBox(_ height: CGFloat) -> some View {
    frame(height: height, alignment: .leading)
  }
}
