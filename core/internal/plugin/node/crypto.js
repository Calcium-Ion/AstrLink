// node:crypto for plugins: digests, HMAC, random values, key derivation,
// ciphers, signatures and key objects, done by the host. A digest keeps its
// state in its object, so nothing outlives it. A cipher gathers what update
// receives and runs in final, so update returns nothing and final returns
// everything; together they return what Node's do.
(function (module, exports, require, host) {
  "use strict";
  const native = host.bind("crypto");
  const errors = require("internal/errors");
  const { invalidArgType, coded, notImplemented } = errors;
  const EventEmitter = require("events");
  const encoder = new TextEncoder();
  let BufferClass;
  const Buffer = () => (BufferClass ??= require("buffer").Buffer);

  const isUtf8 = (encoding) => encoding === undefined || encoding === null || /^utf-?8$/i.test(encoding);
  // bytes is data as bytes, a string read in encoding.
  const bytes = (data, encoding, name = "data") => {
    if (typeof data === "string") return isUtf8(encoding) ? encoder.encode(data) : new Uint8Array(Buffer().from(data, encoding));
    if (ArrayBuffer.isView(data)) return new Uint8Array(data.buffer, data.byteOffset, data.byteLength);
    if (data instanceof ArrayBuffer) return new Uint8Array(data);
    throw invalidArgType(name, "of type string or an instance of Buffer, TypedArray, or DataView", data);
  };
  // output is bytes as Node returns them: a Buffer, or a string in encoding.
  const output = (buffer, encoding) => {
    const result = Buffer().from(buffer);
    return encoding === undefined || encoding === null || encoding === "buffer" ? result : result.toString(encoding);
  };
  const callback = (run, done) => {
    if (typeof done !== "function") throw invalidArgType("callback", "of type function", done);
    let result;
    try {
      result = run();
    } catch (error) {
      queueMicrotask(() => done(error));
      return;
    }
    queueMicrotask(() => done(null, result));
  };

  const kState = Symbol("state");
  const finalized = () => coded(Error, "ERR_CRYPTO_HASH_FINALIZED", "Digest already called");

  // Hash and Hmac take what a stream pipes into them, as Node's do.
  class Digest extends EventEmitter {
    #result;
    write(chunk, encoding, done) {
      this.update(chunk, typeof encoding === "string" ? encoding : undefined);
      if (typeof done === "function") queueMicrotask(() => done(null));
      else if (typeof encoding === "function") queueMicrotask(() => encoding(null));
      return true;
    }
    end(chunk, encoding, done) {
      if (chunk !== undefined && chunk !== null && typeof chunk !== "function") this.update(chunk, typeof encoding === "string" ? encoding : undefined);
      this.#result = this.digest();
      queueMicrotask(() => {
        this.emit("readable");
        this.emit("finish");
        this.emit("end");
        const last = [chunk, encoding, done].find((each) => typeof each === "function");
        last?.();
      });
      return this;
    }
    read() {
      const result = this.#result;
      this.#result = null;
      return result ?? null;
    }
    setEncoding(encoding) {
      const read = this.read.bind(this);
      this.read = () => {
        const result = read();
        return result === null ? null : result.toString(encoding);
      };
      return this;
    }
  }

  class Hash extends Digest {
    #algorithm;
    #state;
    #done = false;
    constructor(algorithm, options, state) {
      super();
      if (typeof algorithm !== "string") throw invalidArgType("algorithm", "of type string", algorithm);
      this.#algorithm = algorithm;
      this.#state = state ?? native.hashNew(algorithm);
    }
    update(data, encoding) {
      if (this.#done) throw finalized();
      this.#state = native.hashUpdate(this.#algorithm, this.#state, bytes(data, encoding));
      return this;
    }
    digest(encoding) {
      if (this.#done) throw finalized();
      this.#done = true;
      return output(native.hashDigest(this.#algorithm, this.#state), encoding);
    }
    copy() {
      if (this.#done) throw finalized();
      return new Hash(this.#algorithm, undefined, this.#state.slice(0));
    }
  }

  class Hmac extends Digest {
    #algorithm;
    #state;
    #pad;
    #done = false;
    constructor(algorithm, key, options) {
      super();
      if (typeof algorithm !== "string") throw invalidArgType("hmac", "of type string", algorithm);
      const keyBytes = key instanceof KeyObject ? bytes(key.export()) : bytes(key, options?.encoding, "key");
      [this.#state, this.#pad] = native.hmacNew(algorithm, keyBytes);
      this.#algorithm = algorithm;
    }
    update(data, encoding) {
      if (this.#done) throw coded(Error, "ERR_CRYPTO_HASH_FINALIZED", "Digest already called");
      this.#state = native.hashUpdate(this.#algorithm, this.#state, bytes(data, encoding));
      return this;
    }
    digest(encoding) {
      if (this.#done) return output(new ArrayBuffer(0), encoding);
      this.#done = true;
      return output(native.hmacDigest(this.#algorithm, this.#state, this.#pad), encoding);
    }
  }

  // KeyObject holds a secret, or an asymmetric key as PEM.
  class KeyObject {
    #type;
    #secret;
    #pem;
    #info;
    constructor(type, secret, pem, info) {
      this.#type = type;
      this.#secret = secret;
      this.#pem = pem;
      this.#info = info;
    }
    get type() {
      return this.#type;
    }
    get symmetricKeySize() {
      return this.#type === "secret" ? this.#secret.byteLength : undefined;
    }
    get asymmetricKeyType() {
      return this.#info?.asymmetricKeyType;
    }
    get asymmetricKeyDetails() {
      if (this.#info === undefined) return undefined;
      const { modulusLength, publicExponent, namedCurve } = this.#info;
      const details = {};
      if (modulusLength !== undefined) details.modulusLength = modulusLength;
      if (publicExponent !== undefined) details.publicExponent = BigInt(publicExponent);
      if (namedCurve !== undefined) details.namedCurve = namedCurve;
      return details;
    }
    export(options) {
      if (this.#type === "secret") {
        if (options?.format === "jwk") return { kty: "oct", k: Buffer().from(this.#secret).toString("base64url") };
        return Buffer().from(this.#secret);
      }
      if (options?.format === "jwk" || options?.format === "der") throw notImplemented(`keyObject.export() as ${options.format}`);
      return this.#pem;
    }
    equals(other) {
      if (!(other instanceof KeyObject) || other.type !== this.#type) return false;
      if (this.#type === "secret") return native.timingSafeEqual(this.#secret, other.#secret) && this.#secret.byteLength === other.#secret.byteLength;
      return this.#pem === other.#pem;
    }
    static from() {
      throw notImplemented("KeyObject.from()");
    }
    get [Symbol.toStringTag]() {
      return "KeyObject";
    }
  }
  const pemOf = (key) => {
    if (key instanceof KeyObject) return key.export();
    if (typeof key === "string") return key;
    if (ArrayBuffer.isView(key)) return new TextDecoder().decode(key);
    if (key !== null && typeof key === "object") {
      if (key.format === "jwk") throw notImplemented("keys in JWK");
      if (key.passphrase !== undefined) throw notImplemented("encrypted private keys");
      return pemOf(key.key);
    }
    throw invalidArgType("key", "of type string or an instance of ArrayBuffer, Buffer, TypedArray, DataView, KeyObject, or CryptoKey", key);
  };
  const createSecretKey = (key, encoding) => new KeyObject("secret", bytes(key, encoding, "key").slice(), undefined, undefined);
  const createPrivateKey = (key) => {
    const pem = pemOf(key);
    return new KeyObject("private", undefined, pem, native.keyInfo(pem, true));
  };
  const createPublicKey = (key) => {
    if (key instanceof KeyObject && key.type === "private") {
      const pem = key.export();
      const info = native.keyInfo(pem, true);
      return new KeyObject("public", undefined, info.publicPem, { ...info, type: "public" });
    }
    const pem = pemOf(key);
    const info = native.keyInfo(pem, false);
    return new KeyObject("public", undefined, info.publicPem ?? pem, info);
  };

  const keyOptions = (key) => {
    const options = key !== null && typeof key === "object" && !(key instanceof KeyObject) && !ArrayBuffer.isView(key) ? key : {};
    return [pemOf(key), options.padding, options.saltLength, options.dsaEncoding];
  };
  const signWith = (algorithm, data, key) => {
    const [pem, padding, saltLength, dsaEncoding] = keyOptions(key);
    return native.sign(algorithm ?? null, pem, data, padding, saltLength, dsaEncoding);
  };
  const verifyWith = (algorithm, data, key, signature) => {
    const [pem, padding, saltLength, dsaEncoding] = keyOptions(key);
    return native.verify(algorithm ?? null, pem, data, padding, saltLength, dsaEncoding, signature);
  };

  // Sign and Verify gather what update receives.
  class Signature extends EventEmitter {
    #algorithm;
    #chunks = [];
    constructor(algorithm) {
      super();
      if (typeof algorithm !== "string") throw invalidArgType("algorithm", "of type string", algorithm);
      this.#algorithm = algorithm;
    }
    update(data, encoding) {
      this.#chunks.push(bytes(data, encoding).slice());
      return this;
    }
    write(chunk, encoding) {
      this.update(chunk, encoding);
      return true;
    }
    end(chunk, encoding) {
      if (chunk !== undefined && chunk !== null) this.update(chunk, encoding);
      return this;
    }
    data() {
      const total = this.#chunks.reduce((sum, chunk) => sum + chunk.byteLength, 0);
      const whole = new Uint8Array(total);
      let at = 0;
      for (const chunk of this.#chunks) {
        whole.set(chunk, at);
        at += chunk.byteLength;
      }
      return whole;
    }
    get algorithm() {
      return this.#algorithm;
    }
  }
  class Sign extends Signature {
    sign(privateKey, encoding) {
      return output(signWith(this.algorithm, this.data(), privateKey), encoding);
    }
  }
  class Verify extends Signature {
    verify(publicKey, signature, encoding) {
      return verifyWith(this.algorithm, this.data(), publicKey, bytes(signature, encoding, "signature"));
    }
  }

  const ciphers = [
    "aes-128-cbc", "aes-192-cbc", "aes-256-cbc", "aes-128-ctr", "aes-192-ctr", "aes-256-ctr",
    "aes-128-gcm", "aes-192-gcm", "aes-256-gcm", "aes-128-ecb", "aes-192-ecb", "aes-256-ecb",
    "chacha20-poly1305",
  ];
  class Cipher extends EventEmitter {
    #algorithm;
    #key;
    #iv;
    #encrypt;
    #chunks = [];
    #aad;
    #tag;
    #padding = true;
    #done = false;
    constructor(algorithm, key, iv, encrypt) {
      super();
      if (typeof algorithm !== "string") throw invalidArgType("cipher", "of type string", algorithm);
      this.#algorithm = algorithm;
      this.#key = key instanceof KeyObject ? bytes(key.export()).slice() : bytes(key, undefined, "key").slice();
      this.#iv = iv === null || iv === undefined ? null : bytes(iv, undefined, "iv").slice();
      this.#encrypt = encrypt;
      if (!ciphers.includes(algorithm.toLowerCase())) throw coded(Error, "ERR_CRYPTO_UNKNOWN_CIPHER", "Unknown cipher");
    }
    update(data, inputEncoding, outputEncoding) {
      if (this.#done) throw coded(Error, "ERR_CRYPTO_INVALID_STATE", "Invalid state for operation update");
      this.#chunks.push(bytes(data, inputEncoding).slice());
      return output(new ArrayBuffer(0), outputEncoding);
    }
    final(outputEncoding) {
      if (this.#done) throw coded(Error, "ERR_CRYPTO_INVALID_STATE", "Invalid state for operation final");
      this.#done = true;
      const total = this.#chunks.reduce((sum, chunk) => sum + chunk.byteLength, 0);
      const data = new Uint8Array(total);
      let at = 0;
      for (const chunk of this.#chunks) {
        data.set(chunk, at);
        at += chunk.byteLength;
      }
      const [result, tag] = native.cipher(this.#algorithm, this.#key, this.#iv ?? new Uint8Array(0), data, this.#encrypt, this.#aad, this.#tag, this.#padding);
      if (tag !== undefined) this.#tag = new Uint8Array(tag);
      return output(result, outputEncoding);
    }
    setAAD(buffer) {
      this.#aad = bytes(buffer, undefined, "buffer").slice();
      return this;
    }
    setAutoPadding(padding = true) {
      this.#padding = Boolean(padding);
      return this;
    }
    getAuthTag() {
      if (!this.#encrypt || !this.#done || this.#tag === undefined)
        throw coded(Error, "ERR_CRYPTO_INVALID_STATE", "Invalid state for operation getAuthTag");
      return Buffer().from(this.#tag);
    }
    setAuthTag(tag) {
      if (this.#encrypt) throw coded(Error, "ERR_CRYPTO_INVALID_STATE", "Invalid state for operation setAuthTag");
      this.#tag = bytes(tag, undefined, "buffer").slice();
      return this;
    }
  }
  class Cipheriv extends Cipher {
    constructor(algorithm, key, iv) {
      super(algorithm, key, iv, true);
    }
  }
  class Decipheriv extends Cipher {
    constructor(algorithm, key, iv) {
      super(algorithm, key, iv, false);
    }
  }

  const randomBytes = (size, done) => {
    if (typeof size !== "number" || size < 0 || size > 2 ** 31 - 1 || !Number.isInteger(size))
      throw errors.outOfRange("size", ">= 0 && <= 2147483647", size);
    const make = () => {
      const buffer = Buffer().alloc(size);
      if (size > 0) native.randomFill(buffer);
      return buffer;
    };
    if (done === undefined) return make();
    callback(make, done);
  };
  const randomFillSync = (buffer, offset = 0, size) => {
    if (!ArrayBuffer.isView(buffer) && !(buffer instanceof ArrayBuffer))
      throw invalidArgType("buf", "an instance of ArrayBuffer or ArrayBufferView", buffer);
    const view = bytes(buffer);
    const end = size === undefined ? view.byteLength : offset + size;
    native.randomFill(view.subarray(offset, end));
    return buffer;
  };
  const randomInt = (min, max, done) => {
    if (max === undefined || typeof max === "function") {
      done = max;
      max = min;
      min = 0;
    }
    if (!Number.isSafeInteger(min)) throw invalidArgType("min", "a safe integer", min);
    if (!Number.isSafeInteger(max)) throw invalidArgType("max", "a safe integer", max);
    if (max <= min) throw errors.outOfRange("max", `greater than the value of "min" (${min})`, max);
    const range = max - min;
    const pick = () => {
      // Rejection sampling over 48 random bits, as Node does.
      const limit = 2 ** 48 - (2 ** 48 % range);
      for (;;) {
        const sample = randomBytes(6).readUIntBE(0, 6);
        if (sample < limit) return min + (sample % range);
      }
    };
    if (done === undefined) return pick();
    callback(pick, done);
  };
  const timingSafeEqual = (a, b) => {
    const first = bytes(a, undefined, "buf1");
    const second = bytes(b, undefined, "buf2");
    if (first.byteLength !== second.byteLength)
      throw coded(RangeError, "ERR_CRYPTO_TIMING_SAFE_EQUAL_LENGTH", "Input buffers must have the same byte length");
    return native.timingSafeEqual(first, second);
  };
  const pbkdf2Sync = (password, salt, iterations, keylen, digest) =>
    Buffer().from(native.pbkdf2(bytes(password, undefined, "password"), bytes(salt, undefined, "salt"), iterations, keylen, digest));
  const scryptSync = (password, salt, keylen, options = {}) => {
    const cost = options.N ?? options.cost ?? 16384;
    const blockSize = options.r ?? options.blockSize ?? 8;
    const parallel = options.p ?? options.parallelization ?? 1;
    return Buffer().from(native.scrypt(bytes(password, undefined, "password"), bytes(salt, undefined, "salt"), keylen, cost, blockSize, parallel));
  };
  const hkdfSync = (digest, key, salt, info, keylen) =>
    native.hkdf(digest, key instanceof KeyObject ? bytes(key.export()) : bytes(key, undefined, "ikm"), bytes(salt, undefined, "salt"), bytes(info, undefined, "info"), keylen);

  const generateKeyPairSync = (type, options = {}) => {
    const [publicPem, privatePem] = native.generateKeyPair(type, options.modulusLength ?? 2048, options.publicExponent ?? 65537, options.namedCurve ?? "");
    const publicKey = createPublicKey(publicPem);
    const privateKey = createPrivateKey(privatePem);
    const encode = (key, encoding) => (encoding?.format === "pem" ? key.export(encoding) : key);
    return { publicKey: encode(publicKey, options.publicKeyEncoding), privateKey: encode(privateKey, options.privateKeyEncoding) };
  };
  const generateKeySync = (type, { length }) => {
    if (type !== "hmac" && type !== "aes") throw errors.invalidArgValue("type", type, "must be a supported key type");
    return createSecretKey(randomBytes(Math.ceil(length / 8)));
  };
  const unsupported = (what) => () => {
    throw notImplemented(what);
  };
  class NotImplemented {
    constructor() {
      throw notImplemented(`crypto.${new.target.name}`);
    }
  }
  const constants = Object.freeze({
    RSA_PKCS1_PADDING: 1,
    RSA_NO_PADDING: 3,
    RSA_PKCS1_OAEP_PADDING: 4,
    RSA_X931_PADDING: 5,
    RSA_PKCS1_PSS_PADDING: 6,
    RSA_PSS_SALTLEN_DIGEST: -1,
    RSA_PSS_SALTLEN_MAX_SIGN: -2,
    RSA_PSS_SALTLEN_AUTO: -2,
    POINT_CONVERSION_COMPRESSED: 2,
    POINT_CONVERSION_UNCOMPRESSED: 4,
    POINT_CONVERSION_HYBRID: 6,
  });

  module.exports = {
    Hash,
    Hmac,
    KeyObject,
    Sign,
    Verify,
    Cipheriv,
    Decipheriv,
    Certificate: class Certificate extends NotImplemented {},
    DiffieHellman: class DiffieHellman extends NotImplemented {},
    ECDH: class ECDH extends NotImplemented {},
    X509Certificate: class X509Certificate extends NotImplemented {},
    constants,
    createHash: (algorithm, options) => new Hash(algorithm, options),
    createHmac: (algorithm, key, options) => new Hmac(algorithm, key, options),
    hash: (algorithm, data, encoding = "hex") => new Hash(algorithm).update(data).digest(encoding),
    getHashes: () => [...native.hashes].sort(),
    getCiphers: () => [...ciphers],
    getCurves: () => ["prime256v1", "secp384r1", "secp521r1"],
    getFips: () => 0,
    setFips: unsupported("crypto.setFips()"),
    randomBytes,
    pseudoRandomBytes: randomBytes,
    randomFillSync,
    randomFill(buffer, offset, size, done) {
      if (typeof offset === "function") [done, offset, size] = [offset, 0, undefined];
      else if (typeof size === "function") [done, size] = [size, undefined];
      callback(() => randomFillSync(buffer, offset, size), done);
    },
    randomInt,
    randomUUID: () => globalThis.crypto.randomUUID(),
    getRandomValues: (array) => globalThis.crypto.getRandomValues(array),
    timingSafeEqual,
    pbkdf2Sync,
    pbkdf2: (password, salt, iterations, keylen, digest, done) => callback(() => pbkdf2Sync(password, salt, iterations, keylen, digest), done),
    scryptSync,
    scrypt(password, salt, keylen, options, done) {
      if (typeof options === "function") [done, options] = [options, {}];
      callback(() => scryptSync(password, salt, keylen, options), done);
    },
    hkdfSync,
    hkdf: (digest, key, salt, info, keylen, done) => callback(() => hkdfSync(digest, key, salt, info, keylen), done),
    createCipheriv: (algorithm, key, iv) => new Cipheriv(algorithm, key, iv),
    createDecipheriv: (algorithm, key, iv) => new Decipheriv(algorithm, key, iv),
    createSign: (algorithm) => new Sign(algorithm),
    createVerify: (algorithm) => new Verify(algorithm),
    sign(algorithm, data, key, done) {
      const run = () => Buffer().from(signWith(algorithm, bytes(data), key));
      if (done === undefined) return run();
      callback(run, done);
    },
    verify(algorithm, data, key, signature, done) {
      const run = () => verifyWith(algorithm, bytes(data), key, bytes(signature, undefined, "signature"));
      if (done === undefined) return run();
      callback(run, done);
    },
    createSecretKey,
    createPrivateKey,
    createPublicKey,
    generateKeyPairSync,
    generateKeyPair(type, options, done) {
      callback(() => generateKeyPairSync(type, options), (error, pair) => (error ? done(error) : done(null, pair.publicKey, pair.privateKey)));
    },
    generateKeySync,
    generateKey: (type, options, done) => callback(() => generateKeySync(type, options), done),
    createECDH: unsupported("crypto.createECDH()"),
    createDiffieHellman: unsupported("crypto.createDiffieHellman()"),
    getDiffieHellman: unsupported("crypto.getDiffieHellman()"),
    publicEncrypt: unsupported("crypto.publicEncrypt()"),
    privateDecrypt: unsupported("crypto.privateDecrypt()"),
    privateEncrypt: unsupported("crypto.privateEncrypt()"),
    publicDecrypt: unsupported("crypto.publicDecrypt()"),
    getCipherInfo: (name) => (ciphers.includes(String(name).toLowerCase()) ? { name: String(name).toLowerCase() } : undefined),
    webcrypto: globalThis.crypto,
    subtle: globalThis.crypto.subtle,
  };
})
