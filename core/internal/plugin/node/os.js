// node:os for plugins: what the system reports, read only. homedir() and
// tmpdir() are the plugin's own directories, the ones its node:fs reaches.
(function (module, exports, require, host) {
  "use strict";
  const native = host.bind("os");
  const info = () => native.info();
  const first = info();
  const windows = first.platform === "win32";
  const signals = {
    SIGHUP: 1, SIGINT: 2, SIGQUIT: 3, SIGILL: 4, SIGTRAP: 5, SIGABRT: 6, SIGIOT: 6, SIGBUS: 7,
    SIGFPE: 8, SIGKILL: 9, SIGUSR1: 10, SIGSEGV: 11, SIGUSR2: 12, SIGPIPE: 13, SIGALRM: 14,
    SIGTERM: 15, SIGCHLD: 17, SIGSTKFLT: 16, SIGCONT: 18, SIGSTOP: 19, SIGTSTP: 20,
    SIGTTIN: 21, SIGTTOU: 22, SIGURG: 23, SIGXCPU: 24, SIGXFSZ: 25, SIGVTALRM: 26,
    SIGPROF: 27, SIGWINCH: 28, SIGIO: 29, SIGPOLL: 29, SIGPWR: 30, SIGSYS: 31,
  };
  const errno = {
    E2BIG: 7, EACCES: 13, EADDRINUSE: 98, EADDRNOTAVAIL: 99, EAGAIN: 11, EBADF: 9, EBUSY: 16,
    ECONNABORTED: 103, ECONNREFUSED: 111, ECONNRESET: 104, EEXIST: 17, EINTR: 4, EINVAL: 22,
    EIO: 5, EISDIR: 21, EMFILE: 24, ENAMETOOLONG: 36, ENOENT: 2, ENOMEM: 12, ENOSPC: 28,
    ENOSYS: 38, ENOTDIR: 20, ENOTEMPTY: 39, ENOTSUP: 95, EPERM: 1, EPIPE: 32, EROFS: 30,
    ETIMEDOUT: 110, EXDEV: 18,
  };
  const constants = Object.freeze({
    UV_UDP_REUSEADDR: 4,
    dlopen: Object.freeze({ RTLD_LAZY: 1, RTLD_NOW: 2, RTLD_GLOBAL: 256, RTLD_LOCAL: 0 }),
    errno: Object.freeze(errno),
    signals: Object.freeze(signals),
    priority: Object.freeze({
      PRIORITY_LOW: 19, PRIORITY_BELOW_NORMAL: 10, PRIORITY_NORMAL: 0,
      PRIORITY_ABOVE_NORMAL: -7, PRIORITY_HIGH: -14, PRIORITY_HIGHEST: -20,
    }),
  });
  const cpus = () => {
    const count = first.cpus;
    return Array.from({ length: count }, () => ({
      model: "unknown",
      speed: 0,
      times: { user: 0, nice: 0, sys: 0, idle: 0, irq: 0 },
    }));
  };
  module.exports = {
    EOL: windows ? "\r\n" : "\n",
    devNull: windows ? "\\\\.\\nul" : "/dev/null",
    constants,
    arch: () => first.arch,
    platform: () => first.platform,
    type: () => first.type,
    release: () => first.release,
    version: () => first.version,
    machine: () => first.machine,
    hostname: () => first.hostname,
    homedir: () => first.homedir,
    tmpdir: () => first.tmpdir,
    endianness: () => "LE",
    cpus,
    availableParallelism: () => first.cpus,
    totalmem: () => info().totalmem,
    freemem: () => info().freemem,
    uptime: () => info().uptime,
    loadavg: () => info().loadavg,
    networkInterfaces: () => ({}),
    userInfo: (options) => {
      const user = { uid: first.uid, gid: first.gid, username: first.username, homedir: first.homedir, shell: first.shell };
      if (options?.encoding === "buffer") {
        const { Buffer } = require("buffer");
        user.username = Buffer.from(user.username);
        user.homedir = Buffer.from(user.homedir);
      }
      return user;
    },
    getPriority: () => 0,
    setPriority: () => {
      throw require("internal/errors").unavailable("os.setPriority()", "plugins cannot change the priority of processes");
    },
  };
})
