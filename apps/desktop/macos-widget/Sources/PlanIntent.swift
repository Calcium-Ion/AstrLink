import AppIntents
import WidgetKit

/// A plan the small widget can show. Plans come from the host's snapshot, in
/// tray order; the extension never asks Core or a provider for anything.
struct PlanEntity: AppEntity {
  static let typeDisplayRepresentation: TypeDisplayRepresentation = "Plan"
  static var defaultQuery: PlanQuery { PlanQuery() }

  var id: String
  var name: String

  var displayRepresentation: DisplayRepresentation {
    DisplayRepresentation(title: "\(name)")
  }

  init(id: String, name: String) {
    self.id = id
    self.name = name
  }

  init(_ plan: QuotaPlan) {
    self.init(id: plan.id, name: plan.name)
  }
}

struct PlanQuery: EntityQuery {
  /// A plan missing from the latest read stays selected; the widget says its
  /// quota is unavailable instead of switching to another plan.
  func entities(for identifiers: [PlanEntity.ID]) async throws -> [PlanEntity] {
    let plans = QuotaSnapshot.load()?.plans ?? []
    return identifiers.map { id in
      plans.first { $0.id == id }.map(PlanEntity.init) ?? PlanEntity(id: id, name: "")
    }
  }

  func suggestedEntities() async throws -> [PlanEntity] {
    (QuotaSnapshot.load()?.plans ?? []).map(PlanEntity.init)
  }

  func defaultResult() async -> PlanEntity? {
    QuotaSnapshot.load()?.plans.first.map(PlanEntity.init)
  }
}

struct SelectPlanIntent: WidgetConfigurationIntent {
  static let title: LocalizedStringResource = "Choose Plan"
  static let description = IntentDescription("Choose the plan this widget shows.")

  @Parameter(title: "Plan")
  var plan: PlanEntity?

  init() {}
}
