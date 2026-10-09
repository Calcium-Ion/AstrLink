import { i18n, useT } from "./i18n";
const en = {
  title: "AstrLink console",
  setup: "Set console password",
  login: "Sign in",
  logout: "Sign out",
  password: "Password",
  confirmation: "Confirm password",
  mismatch: "The passwords do not match.",
  protection:
    "This password also protects saved raw request and response content. Signing in does not unlock that content.",
  countdown: "Setup time remaining",
  expired:
    "Setup time has passed. Restart the container to open a new 10-minute setup window.",
  forgot:
    "Forgot your password? Set ASTRLINK_RESET_PASSWORD to a new value in your deployment configuration and restart. Saved raw content is cleared. API keys and settings stay.",
  resetTitle: "Password reset",
  reset:
    "The password was reset. Saved raw content was cleared. You can remove {variable} from your deployment configuration.",
  retry: "Try again in {seconds} seconds.",
  alreadyConfigured: "A password is already set. Sign in with that password.",
  invalid_password: "Incorrect password.",
  failed: "Unable to connect. Try again.",
  loading: "Loading…",
  retryAction: "Retry",
  seconds: "seconds",
  bounds: "Use {min} to {max} characters.",
  settings: "Console settings",
  language: "Language",
  theme: "Appearance",
  change: "Changing this password signs out your other devices.",
  serverPath: "Local paths refer to files on the server running AstrLink.",
  loopback:
    "This provider needs a callback to localhost on the server. Browser authorization is unavailable in the web console.",
  config: "Connect a client",
  configHint:
    "Copy this configuration to your client. Replace YOUR_ACCESS_TOKEN with an access token from this console. Replace YOUR_MODEL with a model from your provider.",
  copy: "Copy configuration",
  copied: "Copied",
  clipboard: "Clipboard is unavailable. Select and copy the text below.",
  consolePassword: "Console and raw content password",
  close: "Close",
} as const;
const zh: Record<keyof typeof en, string> = {
  title: "AstrLink 控制台",
  setup: "设置控制台口令",
  login: "登录",
  logout: "退出登录",
  password: "口令",
  confirmation: "确认口令",
  mismatch: "两次输入的口令不一致。",
  protection:
    "此口令也保护已保存的请求和响应原文。登录后仍需解锁才能查看原文。",
  countdown: "设置剩余时间",
  expired: "设置时间已过。重启容器可重新开启 10 分钟的设置窗口。",
  forgot:
    "忘记口令时，在部署配置中将 ASTRLINK_RESET_PASSWORD 设为新值并重启。已保存的原文会被清除。API 密钥和设置会保留。",
  resetTitle: "口令已重置",
  reset: "口令已重置。已保存的原文已清除。可以从部署配置中移除 {variable}。",
  retry: "请在 {seconds} 秒后重试。",
  alreadyConfigured: "口令已设置。请使用该口令登录。",
  invalid_password: "口令不正确。",
  failed: "无法连接，请重试。",
  loading: "正在加载…",
  retryAction: "重试",
  seconds: "秒",
  bounds: "请输入 {min} 至 {max} 个字符。",
  settings: "控制台设置",
  language: "语言",
  theme: "外观",
  change: "修改此口令后，其他设备上的控制台会退出登录。",
  serverPath: "本地路径指运行 AstrLink 的服务器上的文件路径。",
  loopback:
    "此提供商需要回调到服务器的 localhost 地址。网页控制台无法完成此授权。",
  config: "连接客户端",
  configHint:
    "将配置复制到客户端。将 YOUR_ACCESS_TOKEN 替换为控制台中的访问令牌。将 YOUR_MODEL 替换为提供商的模型。",
  copy: "复制配置",
  copied: "已复制",
  clipboard: "剪贴板不可用，请选中下方文本复制。",
  consolePassword: "控制台与原文口令",
  close: "关闭",
};
export function webText(
  key: keyof typeof en,
  values: Record<string, string | number> = {},
): string {
  let text: string = (i18n.language.startsWith("zh") ? zh : en)[key];
  for (const [name, value] of Object.entries(values))
    text = text.replaceAll(`{${name}}`, String(value));
  return text;
}
export function useWebText() {
  useT();
  return webText;
}
