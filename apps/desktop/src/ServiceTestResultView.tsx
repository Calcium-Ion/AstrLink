import type { ReactNode } from "react";
import { useT } from "./i18n";
import type { ServiceTestResult } from "./service-test-model";
import { EmptyState } from "./components/EmptyState";
import { FormMessage } from "./components/FormMessage";
import { HelpPopover } from "./components/HelpPopover";
import { Metric, MetricGroup } from "./components/Metric";
import { Panel } from "./components/Panel";
import { ResponseViewer } from "./components/ResponseViewer";
import { StatusBadge } from "./components/StatusBadge";
import { Activity, LoaderCircle } from "./components/icons";
import { Badge } from "./components/ui/badge";
import { ActionGroup } from "./components/ActionGroup";

export function ServiceTestResultView({
  result,
  error,
  running = false,
  blocked,
  stream = true,
  leading,
}: {
  result?: ServiceTestResult | null;
  error?: string | null;
  running?: boolean;
  blocked?: string | null;
  stream?: boolean;
  leading?: ReactNode;
}) {
  const t = useT();
  const formatSeconds = (value: number | null | undefined) =>
    value == null ? "—" : `${(value / 1000).toFixed(2)} s`;
  const firstTokenWait =
    result?.first_token_ms != null && result.response_headers_ms != null
      ? result.first_token_ms - result.response_headers_ms
      : null;
  const failure = error ?? result?.message;
  const status = running
    ? t("serviceTest.testing")
    : result?.ok
      ? t("serviceTest.success")
      : result || error
        ? t("serviceTest.failed")
        : t("serviceTest.ready");
  return (
    <>
      <div className="flex shrink-0 items-center justify-between gap-2">
        {leading ? (
          <div className="flex min-w-0 flex-1 items-center gap-2">
            {leading}
          </div>
        ) : null}
        <ActionGroup className="shrink-0">
          <HelpPopover label={t("serviceTest.timingHelp")} inDialog>
            <p>{t("serviceTest.headersHint")}</p>
            <p className="mt-2">{t("serviceTest.firstTokenHint")}</p>
            <p className="mt-2">{t("serviceTest.totalHint")}</p>
            {!(result?.stream ?? stream) ? (
              <p className="mt-2">{t("serviceTest.nonStreamingTiming")}</p>
            ) : null}
          </HelpPopover>
          <div
            className="flex shrink-0 items-center gap-2"
            role="status"
            aria-live="polite"
          >
            <StatusBadge
              tone={
                running
                  ? "pending"
                  : result?.ok
                    ? "positive"
                    : result || error
                      ? "negative"
                      : "neutral"
              }
            >
              {status}
            </StatusBadge>
            {result && result.status_code > 0 ? (
              <Badge variant="outline">HTTP {result.status_code}</Badge>
            ) : null}
          </div>
        </ActionGroup>
      </div>
      <Panel className="shrink-0">
        <MetricGroup className="grid-cols-4">
          <Metric
            size="sm"
            label={t("serviceTest.headers")}
            value={formatSeconds(result?.response_headers_ms)}
          />
          <Metric
            size="sm"
            label={t("serviceTest.firstTokenWait")}
            value={formatSeconds(firstTokenWait)}
          />
          <Metric
            size="sm"
            label={t("serviceTest.firstToken")}
            value={formatSeconds(result?.first_token_ms)}
            emphasis
          />
          <Metric
            size="sm"
            label={t("serviceTest.total")}
            value={formatSeconds(result?.duration_ms)}
            emphasis
          />
        </MetricGroup>
      </Panel>
      <ResponseViewer
        key={result ? "result" : "empty"}
        content={result?.output}
        rawContent={result?.raw_response}
        rawTruncated={result?.raw_response_truncated}
        contentType={result?.response_content_type}
        label={t("serviceTest.output")}
        notice={
          failure ? (
            <FormMessage tone="error" className="mb-3 break-words">
              {failure}
            </FormMessage>
          ) : undefined
        }
      >
        {!failure ? (
          <EmptyState
            className="h-full border-0 px-2 py-4"
            title={
              blocked
                ? t("serviceTest.unavailable")
                : running
                  ? t("serviceTest.waiting")
                  : t("serviceTest.emptyTitle")
            }
            description={
              blocked ??
              (running
                ? t("serviceTest.running")
                : t("serviceTest.emptyDescription"))
            }
            illustration={
              running ? (
                <LoaderCircle
                  aria-hidden="true"
                  className="size-7 animate-spin text-primary motion-reduce:animate-none"
                />
              ) : (
                <Activity
                  aria-hidden="true"
                  className="size-7 text-muted-foreground"
                />
              )
            }
          />
        ) : null}
      </ResponseViewer>
    </>
  );
}
