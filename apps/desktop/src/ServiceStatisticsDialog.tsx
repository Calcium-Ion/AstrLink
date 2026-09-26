import { useEffect, useState } from "react";
import { invoke } from "@tauri-apps/api/core";
import type { Service } from "./service-model";
import { getServiceBilling } from "./pricing-bridge";
import {
  formatUSD,
  resolveCatalogPrice,
  catalogRateRows,
  OFFICIAL_PROVIDERS,
  type CatalogPrice,
  type ModelRates,
  type PricingConfig,
} from "./pricing-model";
import { Button } from "./components/ui/button";
import { IconButton } from "./components/IconButton";
import { CircleDollarSign } from "./components/icons";
import { Input } from "./components/ui/input";
import {
  Dialog,
  DialogContent,
  DialogTitle,
  DialogDescription,
} from "./components/ui/dialog";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "./components/ui/tabs";
import { Field } from "./components/Field";
import { FilterSelect } from "./components/FilterSelect";
import { ModelSelect } from "./components/ModelSelect";
import { Panel } from "./components/Panel";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "./components/ui/table";

interface Totals {
  records: number;
  priced: number;
  unpriced: number;
  amount_usd: string;
  per_million_usd: string | null;
  cost_samples: number;
  input_tokens: number;
  output_tokens: number;
  cache_samples: number;
  cache_hits: number;
  cache_read_tokens: number;
  cache_input_tokens: number;
  first_token_ms: number | null;
  first_token_samples: number;
  duration_ms: number | null;
  duration_samples: number;
  tps: number | null;
  tps_samples: number;
}
interface Statistics extends Totals {
  from: string;
  to: string;
  models: (Totals & { model: string })[];
}
const percent = (n: number, d: number) =>
  d > 0 ? `${((n / d) * 100).toFixed(1)}%` : "—";
const amount = (s: Totals) => (s.priced ? formatUSD(s.amount_usd) : "—");
const number = (n: number) => n.toLocaleString();
const ms = (n: number | null) =>
  n == null ? "—" : `${(n / 1000).toFixed(2)}s`;
const localDate = (date: Date) =>
  new Date(date.getTime() - date.getTimezoneOffset() * 60000)
    .toISOString()
    .slice(0, 16);

export function ServiceStatisticsEntry({
  service,
  disabled,
}: {
  service: Service;
  disabled: boolean;
}) {
  const [open, setOpen] = useState(false);
  return (
    <>
      <IconButton
        label={`缓存与费用 · ${service.name}`}
        disabled={disabled}
        onClick={() => setOpen(true)}
      >
        <CircleDollarSign aria-hidden="true" />
      </IconButton>
      {open && (
        <ServiceStatisticsDialog
          service={service}
          onClose={() => setOpen(false)}
        />
      )}
    </>
  );
}
function ServiceStatisticsDialog({
  service,
  onClose,
}: {
  service: Service;
  onClose: () => void;
}) {
  const [range, setRange] = useState("1");
  const [from, setFrom] = useState(() =>
    localDate(new Date(Date.now() - 86400000)),
  );
  const [to, setTo] = useState(() => localDate(new Date()));
  const [stats, setStats] = useState<Statistics>();
  const [config, setConfig] = useState<PricingConfig>();
  const [catalog, setCatalog] = useState<CatalogPrice[]>([]);
  const [catalogError, setCatalogError] = useState("");
  const [customize, setCustomize] = useState(false);
  const [model, setModel] = useState(service.models[0] ?? "");
  const [rate, setRate] = useState<ModelRates>({
    input: "",
    output: "",
    cache_read: "",
    cache_write: "",
  });
  const [error, setError] = useState("");
  const [message, setMessage] = useState("");
  const [busy, setBusy] = useState(false);
  const refresh = async () => {
    setBusy(true);
    setError("");
    try {
      const end = range === "custom" ? new Date(to) : new Date();
      const begin =
        range === "custom"
          ? new Date(from)
          : new Date(end.getTime() - Number(range) * 86400000);
      if (
        !Number.isFinite(begin.getTime()) ||
        !Number.isFinite(end.getTime()) ||
        end <= begin ||
        end.getTime() - begin.getTime() > 31 * 86400000
      )
        throw new Error("请选择不超过 31 天的时间范围");
      begin.setMilliseconds(0);
      end.setMilliseconds(0);
      setStats(
        await invoke<Statistics>("service_statistics", {
          serviceId: service.id,
          from: begin.toISOString(),
          to: end.toISOString(),
        }),
      );
    } catch (e) {
      setError(String(e));
    } finally {
      setBusy(false);
    }
  };
  useEffect(() => {
    void refresh();
    void getServiceBilling(service.id)
      .then((r) => setConfig(r.config))
      .catch((e) => setError(String(e)));
    void invoke<{ prices: CatalogPrice[] }>("pricing", { operation: "catalog" })
      .then((value) => setCatalog(value.prices ?? []))
      .catch(() => setCatalogError("价格目录读取失败，请重新打开重试"));
  }, [service.id]);
  useEffect(() => {
    setCustomize(!!config?.overrides?.[model]);
    setRate(
      config?.overrides?.[model] ?? {
        input: "",
        output: "",
        cache_read: "",
        cache_write: "",
      },
    );
  }, [model, config]);
  const catalogPrice = config
    ? resolveCatalogPrice(config, model, catalog)
    : undefined;
  const override = config?.overrides?.[model];
  const effectiveRows = override
    ? [{ label: "渠道自定义价格", rates: override }]
    : catalogRateRows(catalogPrice?.expression ?? "");
  const save = async (remove = false) => {
    if (!config || !model.trim()) return;
    setBusy(true);
    setError("");
    setMessage("");
    try {
      const overrides = { ...config.overrides };
      if (remove) delete overrides[model];
      else overrides[model] = rate;
      const next = { ...config, overrides };
      await invoke("pricing", {
        operation: "configure",
        serviceId: service.id,
        input: next,
      });
      setConfig(next);
      setMessage("模型价格已保存，将用于新请求。已计价的历史费用保持原金额。");
    } catch (e) {
      setError(String(e));
    } finally {
      setBusy(false);
    }
  };
  return (
    <Dialog
      open
      onOpenChange={(v) => {
        if (!v) onClose();
      }}
    >
      <DialogContent variant="workspace" className="max-h-[calc(100dvh-4rem)]">
        <div className="shrink-0 border-b px-4 py-3 pr-12">
          <DialogTitle>缓存与费用 · {service.name}</DialogTitle>
          <DialogDescription className="sr-only">
            按渠道查看缓存命中、费用和模型明细
          </DialogDescription>
        </div>
        <Tabs
          defaultValue="statistics"
          className="flex min-h-0 flex-1 flex-col gap-0 overflow-hidden"
        >
          <div className="shrink-0 border-b px-4 py-2">
            <TabsList>
              <TabsTrigger value="statistics">统计</TabsTrigger>
              <TabsTrigger value="prices">模型价格</TabsTrigger>
            </TabsList>
          </div>
          {error && (
            <p role="alert" className="px-4 py-2 text-sm text-destructive">
              {error}
            </p>
          )}
          <TabsContent
            value="statistics"
            className="m-0 flex min-h-0 flex-1 flex-col overflow-hidden"
          >
            <div className="flex shrink-0 flex-wrap items-center gap-2 px-4 py-2">
              <FilterSelect
                ariaLabel="统计时间范围"
                label=""
                value={range}
                onChange={setRange}
                options={[
                  { value: "1", label: "最近 24 小时" },
                  { value: "7", label: "最近 7 天" },
                  { value: "30", label: "最近 30 天" },
                  { value: "custom", label: "自定义" },
                ]}
              />
              {range === "custom" && (
                <>
                  <Input
                    aria-label="开始时间"
                    type="datetime-local"
                    className="w-auto"
                    value={from}
                    onChange={(e) => setFrom(e.target.value)}
                  />
                  <Input
                    aria-label="结束时间"
                    type="datetime-local"
                    className="w-auto"
                    value={to}
                    onChange={(e) => setTo(e.target.value)}
                  />
                </>
              )}
              <Button size="sm" disabled={busy} onClick={() => void refresh()}>
                {busy ? "加载中" : "查询 / 刷新"}
              </Button>
            </div>
            <div
              className="min-h-0 flex-1 overflow-y-auto p-4 pt-1"
              data-statistics-primary
            >
              {stats && (
                <>
                  <p className="mb-3 text-xs text-muted-foreground">
                    {new Date(stats.from).toLocaleString()} —{" "}
                    {new Date(stats.to).toLocaleString()}
                  </p>
                  <div className="grid grid-cols-2 gap-2 lg:grid-cols-4">
                    {[
                      ["总费用", amount(stats)],
                      [
                        "每 1M Token 费用",
                        formatUSD(stats.per_million_usd ?? undefined),
                      ],
                      [
                        "Token 缓存命中率",
                        percent(
                          stats.cache_read_tokens,
                          stats.cache_input_tokens,
                        ),
                      ],
                      [
                        "缓存命中请求占比",
                        percent(stats.cache_hits, stats.cache_samples),
                      ],
                      [
                        "缓存读取 / 总输入 Token",
                        `${number(stats.cache_read_tokens)} / ${number(stats.cache_input_tokens)}`,
                      ],
                      [
                        "缓存命中 / 有效请求",
                        `${number(stats.cache_hits)} / ${number(stats.cache_samples)}`,
                      ],
                      [
                        "输入 / 输出 Token",
                        `${number(stats.input_tokens)} / ${number(stats.output_tokens)}`,
                      ],
                      [
                        "平均首字 / 耗时 / TPS",
                        `${ms(stats.first_token_ms)} / ${ms(stats.duration_ms)} / ${stats.tps?.toFixed(1) ?? "—"}`,
                      ],
                    ].map(([label, value]) => (
                      <Panel key={label} className="p-3">
                        <div className="text-xs text-muted-foreground">
                          {label}
                        </div>
                        <div className="mt-1 break-words text-sm font-semibold tabular-nums">
                          {value}
                        </div>
                      </Panel>
                    ))}
                  </div>
                  <div className="mt-4 overflow-x-auto">
                    <Table>
                      <TableHeader>
                        <TableRow>
                          {[
                            "模型",
                            "请求",
                            "费用",
                            "每 1M Token",
                            "输入 / 输出",
                            "Token 命中率",
                            "请求命中率",
                            "首字",
                            "耗时",
                            "TPS",
                          ].map((h) => (
                            <TableHead key={h} className="whitespace-nowrap">
                              {h}
                            </TableHead>
                          ))}
                        </TableRow>
                      </TableHeader>
                      <TableBody>
                        {stats.models.map((row) => (
                          <TableRow key={row.model}>
                            <TableCell className="max-w-48 break-words">
                              {row.model || "未知模型"}
                            </TableCell>
                            <TableCell>{number(row.records)}</TableCell>
                            <TableCell>{amount(row)}</TableCell>
                            <TableCell>
                              {formatUSD(row.per_million_usd ?? undefined)}
                            </TableCell>
                            <TableCell>
                              {number(row.input_tokens)} /{" "}
                              {number(row.output_tokens)}
                            </TableCell>
                            <TableCell>
                              {percent(
                                row.cache_read_tokens,
                                row.cache_input_tokens,
                              )}
                            </TableCell>
                            <TableCell>
                              {percent(row.cache_hits, row.cache_samples)}
                            </TableCell>
                            <TableCell>{ms(row.first_token_ms)}</TableCell>
                            <TableCell>{ms(row.duration_ms)}</TableCell>
                            <TableCell>{row.tps?.toFixed(1) ?? "—"}</TableCell>
                          </TableRow>
                        ))}
                      </TableBody>
                    </Table>
                  </div>
                  {!stats.records && (
                    <p className="py-6 text-center text-sm text-muted-foreground">
                      此时间范围暂无请求记录
                    </p>
                  )}
                  <p className="mt-3 text-xs text-muted-foreground">
                    总记录 {stats.records} · 缓存统计排除{" "}
                    {stats.records - stats.cache_samples} · 未计价{" "}
                    {stats.unpriced} · 每百万 Token 统计样本{" "}
                    {stats.cost_samples}
                  </p>
                  <details className="mt-2 text-xs text-muted-foreground">
                    <summary className="cursor-pointer">统计口径与样本</summary>
                    <p className="mt-2">
                      输入与输出 Token、每百万 Token
                      费用取同一批可计价记录。缓存字段缺失的记录不计入命中率。每次实际上游调用单独统计；重试按实际渠道归属。TPS
                      为各请求输出 Token /
                      完整请求秒数的平均值。历史请求明细清理后，费用和 Token
                      仍保留，耗时仅取现存明细。
                    </p>
                    <p className="mt-1">
                      首字样本 {stats.first_token_samples} · 耗时样本{" "}
                      {stats.duration_samples} · TPS 样本 {stats.tps_samples}
                    </p>
                  </details>
                </>
              )}
            </div>
          </TabsContent>
          <TabsContent
            value="prices"
            className="m-0 min-h-0 flex-1 overflow-y-auto p-4"
            data-statistics-primary
          >
            <fieldset
              disabled={busy || !config}
              className="grid max-w-2xl gap-4"
            >
              <Field label="渠道模型">
                <ModelSelect
                  aria-label="价格对应模型"
                  value={model}
                  options={service.models}
                  onValueChange={setModel}
                />
              </Field>
              <p className="text-xs text-muted-foreground">
                价格单位：USD / 1M Token。渠道自定义价格优先于价格目录。
              </p>
              <Panel className="grid gap-3 p-3">
                <div className="text-sm font-medium">当前生效价格</div>
                <p className="text-xs text-muted-foreground">
                  {override
                    ? "来源：渠道自定义价格"
                    : catalogPrice
                      ? `来源：价格目录 · ${OFFICIAL_PROVIDERS[catalogPrice.provider] ?? catalogPrice.provider} · ${catalogPrice.name}`
                      : catalogError ||
                        "未配置价格：该模型没有自定义价格，也未匹配到目录价格；新请求将记录为未计价。"}
                </p>
                {effectiveRows.map((row) => (
                  <div key={row.label} className="grid gap-2">
                    <span className="text-xs font-medium">{row.label}</span>
                    <div className="grid grid-cols-2 gap-2 sm:grid-cols-4">
                      {(
                        [
                          ["input", "输入"],
                          ["output", "输出"],
                          ["cache_read", "缓存读取"],
                          ["cache_write", "缓存写入"],
                        ] as const
                      ).map(([key, label]) => (
                        <div
                          key={key}
                          className="text-xs text-muted-foreground"
                        >
                          {label}
                          <div className="font-mono text-sm text-foreground">
                            ${row.rates[key]}
                          </div>
                        </div>
                      ))}
                    </div>
                  </div>
                ))}
                {!override && catalogPrice && (
                  <details className="text-xs text-muted-foreground">
                    <summary>查看完整目录计价规则</summary>
                    <p className="mt-2 break-words font-mono">
                      {catalogPrice.expression}
                    </p>
                    <p className="mt-1">
                      p 输入 · c 输出 · cr 缓存读取 · cc 缓存写入；按每百万
                      Token 计价。
                    </p>
                  </details>
                )}
              </Panel>
              {!customize && (
                <Button
                  type="button"
                  size="sm"
                  variant="outline"
                  onClick={() => setCustomize(true)}
                >
                  设置渠道自定义价格
                </Button>
              )}
              {customize && (
                <>
                  <p className="text-sm font-medium">
                    渠道自定义价格（保存后覆盖目录价格）
                  </p>
                  <div className="grid gap-3 sm:grid-cols-2">
                    {(
                      [
                        ["input", "输入"],
                        ["output", "输出"],
                        ["cache_read", "缓存读取"],
                        ["cache_write", "缓存写入"],
                      ] as const
                    ).map(([key, label]) => (
                      <Field key={key} label={`${label}（USD / 1M）`}>
                        <Input
                          aria-label={`${label}价格`}
                          inputMode="decimal"
                          value={rate[key]}
                          onChange={(e) =>
                            setRate({ ...rate, [key]: e.target.value })
                          }
                        />
                      </Field>
                    ))}
                  </div>
                  <div className="flex gap-2">
                    <Button
                      size="sm"
                      disabled={!model.trim()}
                      onClick={() => void save()}
                    >
                      保存模型价格
                    </Button>
                    <Button
                      size="sm"
                      variant="outline"
                      disabled={!config?.overrides?.[model]}
                      onClick={() => void save(true)}
                    >
                      恢复价格目录
                    </Button>
                  </div>
                </>
              )}
              {message && (
                <p role="status" className="text-sm text-muted-foreground">
                  {message}
                </p>
              )}
            </fieldset>
          </TabsContent>
        </Tabs>
      </DialogContent>
    </Dialog>
  );
}
