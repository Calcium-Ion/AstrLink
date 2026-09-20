import { useMemo } from "react";
import { Panel } from "@/components/Panel";
import { HelpDisclosure } from "@/components/HelpDisclosure";
import { FormMessage } from "@/components/FormMessage";
import { StatusBadge } from "@/components/StatusBadge";
import { TextExcerpt } from "@/components/TextExcerpt";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { useT } from "./i18n";
import { createDryRunFindingResolver, dryRunSampleText, type DryRunTextSpan } from "./privacy-dry-run-model";
import type { PrivacyDetector, PrivacyDryRunFinding, PrivacyDryRunProtocol, PrivacyDryRunResult as Result } from "./privacy-policy-model";

export interface CompletedPrivacyDryRun extends Result {
  protocol: PrivacyDryRunProtocol;
  detector: PrivacyDetector;
  minConfidence: number;
}

function prettyJSON(body: string): string {
  try { return JSON.stringify(JSON.parse(body), null, 2); } catch { return body; }
}

export function PrivacyDryRunResult({ result, summary, sample, onLocate }: {
  result: CompletedPrivacyDryRun;
  summary: string;
  sample: string;
  onLocate: (span: DryRunTextSpan) => void;
}) {
  const t = useT();
  const rows = useMemo(() => {
    const resolve = createDryRunFindingResolver(result.inspected_body);
    return [
      ...result.findings.map((finding) => ({ finding, ignored: false })),
      ...result.suppressed_findings.map((finding) => ({ finding, ignored: true })),
    ].map((row) => ({ ...row, span: resolve(row.finding) }));
  }, [result]);
  const preview = dryRunSampleText(result.redacted_body ?? result.inspected_body, result.protocol);
  const reason = (finding: PrivacyDryRunFinding, ignored: boolean) => {
    if (ignored && finding.reason && finding.reason !== "low_confidence") {
      return t(`safety.testReason.${finding.reason}`);
    }
    if (!ignored && result.detector === "regex") return t("safety.testReason.regex");
    return t(ignored ? "safety.testReason.low_confidence" : "safety.testReason.confident", {
      confidence: Number((finding.confidence * 100).toFixed(2)),
      threshold: Number((result.minConfidence * 100).toFixed(2)),
    });
  };
  return (
    <div className="grid min-w-0 gap-4" data-testid="safety-dry-run-result">
      <div className="grid gap-2">
        <div className="flex flex-wrap items-center justify-between gap-2">
          <StatusBadge tone={result.decision === "block" ? "negative" : result.decision === "warn" ? "pending" : "positive"}>
            {t(`privacy.${result.decision}`)}
          </StatusBadge>
          <span className="text-xs text-muted-foreground">{summary}</span>
        </div>
        <p className="text-xs leading-relaxed text-muted-foreground">
          {t("safety.testCounts", { hits: result.findings.length, ignored: result.suppressed_findings.length })}
          {" · "}{t(result.detector === "regex" ? "safety.testRegex" : "safety.testModel")}
        </p>
      </div>
      {result.decision === "block" ? <FormMessage tone="warning">{t("safety.blockedPreview")}</FormMessage> : null}
      {rows.length === 0 ? <FormMessage>{t("safety.testNoMatches")}</FormMessage> : (
        <section className="grid min-w-0 gap-2" aria-label={t("safety.testFindingDetails")}>
          <h3 className="text-xs font-medium">{t("safety.testFindingDetails")}</h3>
          <ul className="grid min-w-0 gap-2">
            {rows.map(({ finding, ignored, span }, index) => {
              const replacement = !ignored && result.decision === "redact" && span
                ? result.redactions?.find((item) => item.kind === finding.kind && item.value === span.value)
                : undefined;
              return (
                <Panel asChild key={`${finding.path}:${finding.start}:${finding.end}:${finding.kind}:${index}`}>
                  <li className="grid gap-2 p-3" data-testid="dry-run-finding">
                    <div className="flex flex-wrap items-center gap-2">
                      <strong className="text-sm font-medium">{t(`privacy.${finding.kind}`)}</strong>
                      <Badge variant="secondary">{t(ignored ? "safety.testIgnored" : "safety.passedJudgment")}</Badge>
                      {span && span.text === sample ? (
                        <Button className="ml-auto" size="xs" type="button" variant="ghost" onClick={() => onLocate(span)}>
                          {t("safety.locateOriginal")}
                        </Button>
                      ) : null}
                    </div>
                    {span ? <TextExcerpt {...span} tone={ignored ? "warning" : "primary"} /> : (
                      <p className="text-xs text-muted-foreground">{t("safety.testSpanUnavailable")}</p>
                    )}
                    {replacement ? (
                      <div className="flex min-w-0 items-start gap-2 text-xs">
                        <span className="shrink-0 text-muted-foreground">{t("safety.testReplaceWith")}</span>
                        <code className="whitespace-pre-wrap break-all text-primary">{replacement.placeholder}</code>
                      </div>
                    ) : null}
                    <p className="text-xs leading-relaxed text-text-secondary">
                      {reason(finding, ignored)}
                      {!replacement ? <> {t(ignored ? "safety.testKeepOriginal" : `safety.testAction.${result.decision}`)}</> : null}
                    </p>
                  </li>
                </Panel>
              );
            })}
          </ul>
        </section>
      )}
      {result.decision !== "block" && preview !== null ? (
        <section className="grid min-w-0 gap-2" aria-label={t("safety.testOutputText")}>
          <h3 className="text-xs font-medium">{t("safety.testOutputText")}</h3>
          <Panel tone="inset" className="p-3">
            <p className="whitespace-pre-wrap break-words text-sm leading-relaxed [overflow-wrap:anywhere]">{preview}</p>
          </Panel>
        </section>
      ) : null}
      <HelpDisclosure title={t("safety.testTechnicalDetails")}>
        <p>{result.protocol}</p>
        {rows.map(({ finding }, index) => (
          <p key={index} className="break-all">
            {t(`privacy.${finding.kind}`)} · <code>{finding.path}</code> · {t("safety.testByteRange", { start: finding.start, end: finding.end })}
          </p>
        ))}
        <h3 className="font-medium">{t("safety.requestPreview")}</h3>
        <pre className="whitespace-pre-wrap break-all font-mono text-xs">{prettyJSON(result.inspected_body)}</pre>
        {result.redacted_body !== undefined ? <>
          <h3 className="font-medium">{t("safety.redactedBody")}</h3>
          <pre className="whitespace-pre-wrap break-all font-mono text-xs">{prettyJSON(result.redacted_body)}</pre>
        </> : null}
      </HelpDisclosure>
    </div>
  );
}
