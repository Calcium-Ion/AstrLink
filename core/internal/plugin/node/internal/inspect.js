// util.inspect for plugins: dsh-ptc-moejs's port (MIT) of
// lib/internal/util/inspect.js of Node.js v22.23.1 (MIT). It leaves out what
// moejs code cannot reach: proxy details, the entries of iterators and
// WeakMaps, and external values.
(function (module, exports, require, host) {
module.exports = (function makeInspect(promiseDetails, headerLength) {
  'use strict'

  const {
    defineProperty, getOwnPropertyDescriptor, getOwnPropertyNames, getOwnPropertySymbols, getPrototypeOf, hasOwn, keys: objectKeys,
  } = Object
  const apply = Reflect.apply
  const isArray = Array.isArray
  const getter = (object, key) => getOwnPropertyDescriptor(object, key).get
  const objectToString = Object.prototype.toString
  const propertyIsEnumerable = Object.prototype.propertyIsEnumerable
  const functionToString = Function.prototype.toString
  const errorToString = Error.prototype.toString
  const symbolToString = Symbol.prototype.toString
  const dateGetTime = Date.prototype.getTime
  const dateToISOString = Date.prototype.toISOString
  const dateToString = Date.prototype.toString
  const regExpToString = RegExp.prototype.toString
  const regExpSource = getter(RegExp.prototype, 'source')
  const mapSize = getter(Map.prototype, 'size')
  const setSize = getter(Set.prototype, 'size')
  const mapEntries = Map.prototype.entries
  const setValues = Set.prototype.values
  const typedArrayPrototype = getPrototypeOf(Uint8Array.prototype)
  const typedArrayLength = getter(typedArrayPrototype, 'length')
  const typedArrayTag = getter(typedArrayPrototype, Symbol.toStringTag)
  const arrayBufferByteLength = getter(ArrayBuffer.prototype, 'byteLength')
  const sharedArrayBufferByteLength = typeof SharedArrayBuffer === 'function' ? getter(SharedArrayBuffer.prototype, 'byteLength') : undefined
  const dataViewByteLength = getter(DataView.prototype, 'byteLength')
  const AsyncFunction = (async () => {}).constructor
  const GeneratorFunction = (function* () {}).constructor
  const AsyncGeneratorFunction = (async function* () {}).constructor
  const customInspectSymbol = Symbol.for('nodejs.util.inspect.custom')

  const brand = (fn, ...args) => (value) => {
    try {
      apply(fn, value, args)
      return true
    } catch {
      return false
    }
  }
  const tagOf = value => apply(objectToString, value, []).slice(8, -1)
  const isDate = brand(dateGetTime)
  const isRegExp = value => value !== RegExp.prototype && brand(regExpSource)(value)
  const isMap = brand(Map.prototype.has, undefined)
  const isSet = brand(Set.prototype.has, undefined)
  const isWeakMap = brand(WeakMap.prototype.has, {})
  const isWeakSet = brand(WeakSet.prototype.has, {})
  const isTypedArray = value => apply(typedArrayTag, value, []) !== undefined
  const isArrayBuffer = brand(arrayBufferByteLength)
  const isSharedArrayBuffer = value => sharedArrayBufferByteLength !== undefined && brand(sharedArrayBufferByteLength)(value)
  const isDataView = brand(dataViewByteLength)
  const isPromise = value => value instanceof Promise && promiseDetails(value) !== undefined
  const isError = value => value instanceof Error || tagOf(value) === 'Error'
  const isNumberObject = brand(Number.prototype.valueOf)
  const isStringObject = brand(String.prototype.valueOf)
  const isBooleanObject = brand(Boolean.prototype.valueOf)
  const isBigIntObject = brand(BigInt.prototype.valueOf)
  const isSymbolObject = brand(Symbol.prototype.valueOf)
  const isBoxedPrimitive = value => isNumberObject(value) || isStringObject(value) || isBooleanObject(value) || isBigIntObject(value) || isSymbolObject(value)
  const isArgumentsObject = value => tagOf(value) === 'Arguments'
  const isModuleNamespaceObject = value => getPrototypeOf(value) === null && tagOf(value) === 'Module'
  const isGeneratorFunction = value => value instanceof GeneratorFunction || value instanceof AsyncGeneratorFunction
  const isAsyncFunction = value => value instanceof AsyncFunction || value instanceof AsyncGeneratorFunction

  const inspectDefaultOptions = Object.seal({
    showHidden: false,
    depth: 2,
    colors: false,
    customInspect: true,
    showProxy: false,
    maxArrayLength: 100,
    maxStringLength: 10000,
    breakLength: 80,
    compact: 3,
    sorted: false,
    getters: false,
    numericSeparator: false,
  })

  const kObjectType = 0
  const kArrayType = 1
  const kArrayExtrasType = 2

  const strEscapeSequencesRegExp = /[\x00-\x1f\x27\x5c\x7f-\x9f]|[\ud800-\udbff](?![\udc00-\udfff])|(?<![\ud800-\udbff])[\udc00-\udfff]/
  const strEscapeSequencesReplacer = /[\x00-\x1f\x27\x5c\x7f-\x9f]|[\ud800-\udbff](?![\udc00-\udfff])|(?<![\ud800-\udbff])[\udc00-\udfff]/g
  const strEscapeSequencesRegExpSingle = /[\x00-\x1f\x5c\x7f-\x9f]|[\ud800-\udbff](?![\udc00-\udfff])|(?<![\ud800-\udbff])[\udc00-\udfff]/
  const strEscapeSequencesReplacerSingle = /[\x00-\x1f\x5c\x7f-\x9f]|[\ud800-\udbff](?![\udc00-\udfff])|(?<![\ud800-\udbff])[\udc00-\udfff]/g
  const keyStrRegExp = /^[a-zA-Z_][a-zA-Z_0-9]*$/
  const numberRegExp = /^(0|[1-9][0-9]*)$/
  const classRegExp = /^(\s+[^(]*?)\s*{/
  const stripCommentsRegExp = /(\/\/.*?\n)|(\/\*(.|\n)*?\*\/)/g
  const kMinLineLength = 16

  // Escaped control characters, the single quote and the backslash.
  const meta = [
    '\\x00', '\\x01', '\\x02', '\\x03', '\\x04', '\\x05', '\\x06', '\\x07',
    '\\b', '\\t', '\\n', '\\x0B', '\\f', '\\r', '\\x0E', '\\x0F',
    '\\x10', '\\x11', '\\x12', '\\x13', '\\x14', '\\x15', '\\x16', '\\x17',
    '\\x18', '\\x19', '\\x1A', '\\x1B', '\\x1C', '\\x1D', '\\x1E', '\\x1F',
    '', '', '', '', '', '', '', "\\'", '', '', '', '', '', '', '', '',
    '', '', '', '', '', '', '', '', '', '', '', '', '', '', '', '',
    '', '', '', '', '', '', '', '', '', '', '', '', '', '', '', '',
    '', '', '', '', '', '', '', '', '', '', '', '', '\\\\', '', '', '',
    '', '', '', '', '', '', '', '', '', '', '', '', '', '', '', '',
    '', '', '', '', '', '', '', '', '', '', '', '', '', '', '', '\\x7F',
    '\\x80', '\\x81', '\\x82', '\\x83', '\\x84', '\\x85', '\\x86', '\\x87',
    '\\x88', '\\x89', '\\x8A', '\\x8B', '\\x8C', '\\x8D', '\\x8E', '\\x8F',
    '\\x90', '\\x91', '\\x92', '\\x93', '\\x94', '\\x95', '\\x96', '\\x97',
    '\\x98', '\\x99', '\\x9A', '\\x9B', '\\x9C', '\\x9D', '\\x9E', '\\x9F',
  ]

  const ansi = /[\u001B\u009B][[\]()#;?]*(?:(?:(?:(?:;[-a-zA-Z\d/#&.:=?%@~_]+)*|[a-zA-Z\d]+(?:;[-a-zA-Z\d/#&.:=?%@~_]*)*)?(?:\u0007|\u001B\\|\u009C))|(?:(?:\d{1,4}(?:;\d{0,4})*)?[\dA-PR-TZcf-nq-uy=><~]))/g

  const colors = {
    bold: [1, 22], italic: [3, 23], underline: [4, 24], inverse: [7, 27],
    white: [37, 39], grey: [90, 39], black: [30, 39], blue: [34, 39], cyan: [36, 39], green: [32, 39],
    magenta: [35, 39], red: [31, 39], yellow: [33, 39],
  }
  const styles = {
    special: 'cyan', number: 'yellow', bigint: 'yellow', boolean: 'yellow', undefined: 'grey', null: 'bold',
    string: 'green', symbol: 'green', date: 'magenta', regexp: 'red', module: 'underline',
  }

  // The builtins whose toString util.format's %s may call.
  const builtInObjects = new Set(getOwnPropertyNames(globalThis).filter(name => /^[A-Z][a-zA-Z0-9]+$/.test(name)))

  function inspect(value, opts) {
    const ctx = {
      budget: {},
      indentationLvl: 0,
      seen: [],
      currentDepth: 0,
      stylize: stylizeNoColor,
      ...inspectDefaultOptions,
    }
    if (arguments.length > 1) {
      if (arguments.length > 2) {
        if (arguments[2] !== undefined) ctx.depth = arguments[2]
        if (arguments.length > 3 && arguments[3] !== undefined) ctx.colors = arguments[3]
      }
      if (typeof opts === 'boolean') {
        ctx.showHidden = opts
      } else if (opts) {
        for (const key of objectKeys(opts)) {
          if (hasOwn(inspectDefaultOptions, key) || key === 'stylize') ctx[key] = opts[key]
          else if (ctx.userOptions === undefined) ctx.userOptions = opts
        }
      }
    }
    if (ctx.colors) ctx.stylize = stylizeWithColor
    if (ctx.maxArrayLength === null) ctx.maxArrayLength = Infinity
    if (ctx.maxStringLength === null) ctx.maxStringLength = Infinity
    return formatValue(ctx, value, 0)
  }
  inspect.custom = customInspectSymbol
  defineProperty(inspect, 'defaultOptions', {
    get() { return inspectDefaultOptions },
    set(options) {
      if (options === null || typeof options !== 'object') throw new TypeError('The "options" argument must be of type object.')
      return Object.assign(inspectDefaultOptions, options)
    },
  })
  inspect.colors = colors
  inspect.styles = styles

  function getUserOptions(ctx) {
    const ret = {
      stylize: ctx.stylize,
      showHidden: ctx.showHidden,
      depth: ctx.depth,
      colors: ctx.colors,
      customInspect: ctx.customInspect,
      showProxy: ctx.showProxy,
      maxArrayLength: ctx.maxArrayLength,
      maxStringLength: ctx.maxStringLength,
      breakLength: ctx.breakLength,
      compact: ctx.compact,
      sorted: ctx.sorted,
      getters: ctx.getters,
      numericSeparator: ctx.numericSeparator,
      ...ctx.userOptions,
    }
    return ret
  }

  function addQuotes(str, quotes) {
    if (quotes === -1) return `"${str}"`
    if (quotes === -2) return `\`${str}\``
    return `'${str}'`
  }

  function escapeFn(str) {
    const charCode = str.charCodeAt(0)
    return meta.length > charCode ? meta[charCode] : `\\u${charCode.toString(16)}`
  }

  // Escape control characters, single quotes and the backslash.
  function strEscape(str) {
    let escapeTest = strEscapeSequencesRegExp
    let escapeReplace = strEscapeSequencesReplacer
    let singleQuote = 39
    if (str.includes("'")) {
      if (!str.includes('"')) {
        singleQuote = -1
      } else if (!str.includes('`') && !str.includes('${')) {
        singleQuote = -2
      }
      if (singleQuote !== 39) {
        escapeTest = strEscapeSequencesRegExpSingle
        escapeReplace = strEscapeSequencesReplacerSingle
      }
    }
    if (str.length < 5000 && !escapeTest.test(str)) return addQuotes(str, singleQuote)
    if (str.length > 100) {
      str = str.replace(escapeReplace, escapeFn)
      return addQuotes(str, singleQuote)
    }
    let result = ''
    let last = 0
    for (let i = 0; i < str.length; i++) {
      const point = str.charCodeAt(i)
      if (point === singleQuote || point === 92 || point < 32 || (point > 126 && point < 160)) {
        if (last === i) result += meta[point]
        else result += `${str.slice(last, i)}${meta[point]}`
        last = i + 1
      } else if (point >= 0xd800 && point <= 0xdfff) {
        if (point <= 0xdbff && i + 1 < str.length) {
          const next = str.charCodeAt(i + 1)
          if (next >= 0xdc00 && next <= 0xdfff) {
            i++
            continue
          }
        }
        result += `${str.slice(last, i)}\\u${point.toString(16)}`
        last = i + 1
      }
    }
    if (last !== str.length) result += str.slice(last)
    return addQuotes(result, singleQuote)
  }

  function stylizeWithColor(str, styleType) {
    const style = styles[styleType]
    if (style !== undefined) {
      const color = colors[style]
      if (color !== undefined) return `\u001b[${color[0]}m${str}\u001b[${color[1]}m`
    }
    return str
  }

  function stylizeNoColor(str) {
    return str
  }

  function isInstanceof(object, proto) {
    try {
      return object instanceof proto
    } catch {
      return false
    }
  }

  function getConstructorName(obj, ctx, recurseTimes) {
    let firstProto
    const tmp = obj
    while (obj) {
      const descriptor = getOwnPropertyDescriptor(obj, 'constructor')
      if (descriptor !== undefined && typeof descriptor.value === 'function' && descriptor.value.name !== '' && isInstanceof(tmp, descriptor.value)) {
        return String(descriptor.value.name)
      }
      obj = getPrototypeOf(obj)
      if (firstProto === undefined) firstProto = obj
    }
    if (firstProto === null) return null
    const res = tagOf(tmp)
    if (recurseTimes > ctx.depth && ctx.depth !== null) return `${res} <Complex prototype>`
    const protoConstr = getConstructorName(firstProto, ctx, recurseTimes + 1)
    if (protoConstr === null) {
      return `${res} <${inspect(firstProto, { ...ctx, customInspect: false, depth: -1 })}>`
    }
    return `${res} <${protoConstr}>`
  }

  function getPrefix(constructor, tag, fallback, size = '') {
    if (constructor === null) {
      if (tag !== '' && fallback !== tag) return `[${fallback}${size}: null prototype] [${tag}] `
      return `[${fallback}${size}: null prototype] `
    }
    let result = `${constructor}${size} `
    if (tag !== '') {
      const position = constructor.indexOf(tag)
      if (position === -1) {
        result += `[${tag}] `
      } else {
        const endPos = position + tag.length
        if (endPos !== constructor.length && constructor[endPos] === constructor[endPos].toLowerCase()) result += `[${tag}] `
      }
    }
    return result
  }

  function getKeys(value, showHidden) {
    let keys
    const symbols = getOwnPropertySymbols(value)
    if (showHidden) {
      keys = getOwnPropertyNames(value)
      if (symbols.length !== 0) keys.push(...symbols)
    } else {
      try {
        keys = objectKeys(value)
      } catch {
        keys = getOwnPropertyNames(value)
      }
      if (symbols.length !== 0) keys.push(...symbols.filter(key => apply(propertyIsEnumerable, value, [key])))
    }
    return keys
  }

  const isIndex = key => numberRegExp.test(key) && +key < 2 ** 32 - 1

  // The own keys of an array or a typed array that are not indices. The
  // keys of a very long one are not listed, which would cost its length.
  function getOwnNonIndexProperties(value, showHidden, length) {
    const out = []
    if (length > 100000) return out
    for (const key of Reflect.ownKeys(value)) {
      if (typeof key === 'string' && isIndex(key)) continue
      if (!showHidden && !apply(propertyIsEnumerable, value, [key])) continue
      out.push(key)
    }
    return out
  }

  function getCtxStyle(value, constructor, tag) {
    let fallback = ''
    if (constructor === null) {
      fallback = tagOf(value)
      if (fallback === tag) fallback = 'Object'
    }
    return getPrefix(constructor, tag, fallback)
  }

  function formatValue(ctx, value, recurseTimes, typedArray) {
    if (typeof value !== 'object' && typeof value !== 'function') return formatPrimitive(ctx.stylize, value, ctx)
    if (value === null) return ctx.stylize('null', 'null')
    const context = value
    if (ctx.customInspect) {
      const maybeCustom = value[customInspectSymbol]
      if (typeof maybeCustom === 'function' && maybeCustom !== inspect &&
          getOwnPropertyDescriptor(value, 'constructor')?.value?.prototype !== value) {
        const depth = ctx.depth === null ? null : ctx.depth - recurseTimes
        const ret = apply(maybeCustom, context, [depth, getUserOptions(ctx), inspect])
        if (ret !== context) {
          if (typeof ret !== 'string') return formatValue(ctx, ret, recurseTimes)
          return ret.replaceAll('\n', `\n${' '.repeat(ctx.indentationLvl)}`)
        }
      }
    }
    if (ctx.seen.includes(value)) {
      let index = 1
      if (ctx.circular === undefined) {
        ctx.circular = new Map()
        ctx.circular.set(value, index)
      } else {
        index = ctx.circular.get(value)
        if (index === undefined) {
          index = ctx.circular.size + 1
          ctx.circular.set(value, index)
        }
      }
      return ctx.stylize(`[Circular *${index}]`, 'special')
    }
    return formatRaw(ctx, value, recurseTimes, typedArray)
  }

  function formatRaw(ctx, value, recurseTimes, typedArray) {
    let keys
    const constructor = getConstructorName(value, ctx, recurseTimes)
    let tag = value[Symbol.toStringTag]
    // Only list the tag when it is not an own (enumerable) property, which would print it twice.
    if (typeof tag !== 'string' || (tag !== '' && apply(ctx.showHidden ? hasOwnProperty : propertyIsEnumerable, value, [Symbol.toStringTag]))) {
      tag = ''
    }
    let base = ''
    let formatter = getEmptyFormatArray
    let braces
    let noIterator = true
    let extrasType = kObjectType
    let extraKeys

    if (Symbol.iterator in value || constructor === null) {
      noIterator = false
      if (isArray(value)) {
        const prefix = (constructor !== 'Array' || tag !== '') ? getPrefix(constructor, tag, 'Array', `(${value.length})`) : ''
        keys = getOwnNonIndexProperties(value, ctx.showHidden, value.length)
        braces = [`${prefix}[`, ']']
        if (value.length === 0 && keys.length === 0) return `${braces[0]}]`
        extrasType = kArrayExtrasType
        formatter = formatArray
      } else if (isSet(value)) {
        const size = apply(setSize, value, [])
        const prefix = getPrefix(constructor, tag, 'Set', `(${size})`)
        keys = getKeys(value, ctx.showHidden)
        const values = constructor !== null ? value : apply(setValues, value, [])
        formatter = (ctx, ignored, recurseTimes) => formatSet(values, size, ctx, recurseTimes)
        if (size === 0 && keys.length === 0) return `${prefix}{}`
        braces = [`${prefix}{`, '}']
      } else if (isMap(value)) {
        const size = apply(mapSize, value, [])
        const prefix = getPrefix(constructor, tag, 'Map', `(${size})`)
        keys = getKeys(value, ctx.showHidden)
        const entries = constructor !== null ? value : apply(mapEntries, value, [])
        formatter = (ctx, ignored, recurseTimes) => formatMap(entries, size, ctx, recurseTimes)
        if (size === 0 && keys.length === 0) return `${prefix}{}`
        braces = [`${prefix}{`, '}']
      } else if (isTypedArray(value)) {
        const size = apply(typedArrayLength, value, [])
        keys = getOwnNonIndexProperties(value, ctx.showHidden, size)
        const fallback = constructor === null ? apply(typedArrayTag, value, []) : ''
        const prefix = getPrefix(constructor, tag, fallback, `(${size})`)
        braces = [`${prefix}[`, ']']
        if (size === 0 && keys.length === 0 && !ctx.showHidden) return `${braces[0]}]`
        formatter = ctx => formatTypedArray(value, size, ctx)
        extrasType = kArrayExtrasType
        if (ctx.showHidden) {
          extraKeys = ['BYTES_PER_ELEMENT', 'length', 'byteLength', 'byteOffset', 'buffer']
          typedArray = true
        }
      } else {
        noIterator = true
      }
    }
    if (noIterator) {
      keys = getKeys(value, ctx.showHidden)
      braces = ['{', '}']
      if (typeof value === 'function') {
        base = getFunctionBase(ctx, value, constructor, tag)
        if (keys.length === 0) return ctx.stylize(base, 'special')
      } else if (constructor === 'Object') {
        if (isArgumentsObject(value)) {
          braces[0] = '[Arguments] {'
        } else if (tag !== '') {
          braces[0] = `${getPrefix(constructor, tag, 'Object')}{`
        }
        if (keys.length === 0) return `${braces[0]}}`
      } else if (isRegExp(value)) {
        base = apply(regExpToString, constructor !== null ? value : new RegExp(value), [])
        const prefix = getPrefix(constructor, tag, 'RegExp')
        if (prefix !== 'RegExp ') base = `${prefix}${base}`
        if (keys.length === 0 || (recurseTimes > ctx.depth && ctx.depth !== null)) return ctx.stylize(base, 'regexp')
      } else if (isDate(value)) {
        base = Number.isNaN(apply(dateGetTime, value, [])) ? apply(dateToString, value, []) : apply(dateToISOString, value, [])
        const prefix = getPrefix(constructor, tag, 'Date')
        if (prefix !== 'Date ') base = `${prefix}${base}`
        if (keys.length === 0) return ctx.stylize(base, 'date')
      } else if (isError(value)) {
        base = formatError(value, constructor, tag, ctx, keys)
        if (keys.length === 0) return base
      } else if (isArrayBuffer(value) || isSharedArrayBuffer(value)) {
        const arrayType = isArrayBuffer(value) ? 'ArrayBuffer' : 'SharedArrayBuffer'
        const prefix = getPrefix(constructor, tag, arrayType)
        if (typedArray === undefined) {
          formatter = formatArrayBuffer
        } else if (keys.length === 0) {
          return prefix + `{ [byteLength]: ${formatNumber(ctx.stylize, value.byteLength, false)} }`
        }
        braces[0] = `${prefix}{`
        extraKeys = ['byteLength']
      } else if (isDataView(value)) {
        braces[0] = `${getPrefix(constructor, tag, 'DataView')}{`
        extraKeys = ['byteLength', 'byteOffset', 'buffer']
      } else if (isPromise(value)) {
        braces[0] = `${getPrefix(constructor, tag, 'Promise')}{`
        formatter = formatPromise
      } else if (isWeakSet(value)) {
        braces[0] = `${getPrefix(constructor, tag, 'WeakSet')}{`
        formatter = formatWeakCollection
      } else if (isWeakMap(value)) {
        braces[0] = `${getPrefix(constructor, tag, 'WeakMap')}{`
        formatter = formatWeakCollection
      } else if (isModuleNamespaceObject(value)) {
        braces[0] = `${getPrefix(constructor, tag, 'Module')}{`
        formatter = (ctx, value, recurseTimes) => formatNamespaceObject(keys, ctx, value, recurseTimes)
      } else if (isBoxedPrimitive(value)) {
        base = getBoxedBase(value, ctx, keys, constructor, tag)
        if (keys.length === 0) return base
      } else {
        if (keys.length === 0) return `${getCtxStyle(value, constructor, tag)}{}`
        braces[0] = `${getCtxStyle(value, constructor, tag)}{`
      }
    }

    if (recurseTimes > ctx.depth && ctx.depth !== null) {
      let constructorName = getCtxStyle(value, constructor, tag).slice(0, -1)
      if (constructor !== null) constructorName = `[${constructorName}]`
      return ctx.stylize(constructorName, 'special')
    }
    recurseTimes += 1
    ctx.seen.push(value)
    ctx.currentDepth = recurseTimes
    let output
    const indentationLvl = ctx.indentationLvl
    try {
      output = formatter(ctx, value, recurseTimes)
      if (extraKeys !== undefined) {
        for (let i = 0; i < extraKeys.length; i++) {
          let formatted
          try {
            formatted = formatExtraProperties(ctx, value, recurseTimes, extraKeys[i], typedArray)
          } catch {
            const tempValue = { [extraKeys[i]]: value.buffer[extraKeys[i]] }
            formatted = formatExtraProperties(ctx, tempValue, recurseTimes, extraKeys[i], typedArray)
          }
          output.push(formatted)
        }
      }
      for (let i = 0; i < keys.length; i++) {
        output.push(formatProperty(ctx, value, recurseTimes, keys[i], extrasType))
      }
    } catch (err) {
      if (!(err instanceof RangeError) || !/call stack/i.test(err.message)) throw err
      return handleMaxCallStackSize(ctx, getCtxStyle(value, constructor, tag).slice(0, -1), indentationLvl)
    }
    if (ctx.circular !== undefined) {
      const index = ctx.circular.get(value)
      if (index !== undefined) {
        const reference = ctx.stylize(`<ref *${index}>`, 'special')
        if (ctx.compact !== true) base = base === '' ? reference : `${reference} ${base}`
        else braces[0] = `${reference} ${braces[0]}`
      }
    }
    ctx.seen.pop()

    if (ctx.sorted) {
      const comparator = ctx.sorted === true ? undefined : ctx.sorted
      if (extrasType === kObjectType) {
        output.sort(comparator)
      } else if (keys.length > 1) {
        const sorted = output.slice(output.length - keys.length).sort(comparator)
        output.splice(output.length - keys.length, keys.length, ...sorted)
      }
    }

    const res = reduceToSingleString(ctx, output, base, braces, extrasType, recurseTimes, value)
    const budget = ctx.budget[ctx.indentationLvl] || 0
    const newLength = budget + res.length
    ctx.budget[ctx.indentationLvl] = newLength
    if (newLength > 2 ** 27) ctx.depth = -1
    return res
  }

  function hasOwnProperty(key) {
    return hasOwn(this, key)
  }

  function getEmptyFormatArray() {
    return []
  }

  function getBoxedBase(value, ctx, keys, constructor, tag) {
    let fn
    let type
    if (isNumberObject(value)) {
      fn = Number.prototype.valueOf
      type = 'Number'
    } else if (isStringObject(value)) {
      fn = String.prototype.valueOf
      type = 'String'
      // The indices of a boxed string repeat its value.
      keys.splice(0, value.length)
    } else if (isBooleanObject(value)) {
      fn = Boolean.prototype.valueOf
      type = 'Boolean'
    } else if (isBigIntObject(value)) {
      fn = BigInt.prototype.valueOf
      type = 'BigInt'
    } else {
      fn = Symbol.prototype.valueOf
      type = 'Symbol'
    }
    let base = `[${type}`
    if (type !== constructor) {
      if (constructor === null) base += ' (null prototype)'
      else base += ` (${constructor})`
    }
    base += `: ${formatPrimitive(stylizeNoColor, apply(fn, value, []), ctx)}]`
    if (tag !== '' && tag !== constructor) base += ` [${tag}]`
    if (keys.length !== 0 || ctx.stylize === stylizeNoColor) return base
    return ctx.stylize(base, type.toLowerCase())
  }

  function getClassBase(value, constructor, tag) {
    const name = (hasOwn(value, 'name') && value.name) || '(anonymous)'
    let base = `class ${name}`
    if (constructor !== 'Function' && constructor !== null) base += ` [${constructor}]`
    if (tag !== '' && constructor !== tag) base += ` [${tag}]`
    if (constructor !== null) {
      const superName = getPrototypeOf(value).name
      if (superName) base += ` extends ${superName}`
    } else {
      base += ' extends [null prototype]'
    }
    return `[${base}]`
  }

  function getFunctionBase(ctx, value, constructor, tag) {
    const stringified = apply(functionToString, value, [])
    if (stringified.startsWith('class') && stringified[stringified.length - 1] === '}') {
      const slice = stringified.slice(5, -1)
      const bracketIndex = slice.indexOf('{')
      if (bracketIndex !== -1 && (!slice.slice(0, bracketIndex).includes('(') || classRegExp.exec(slice.replace(stripCommentsRegExp, '')) !== null)) {
        return getClassBase(value, constructor, tag)
      }
    }
    let type = 'Function'
    if (isGeneratorFunction(value)) type = `Generator${type}`
    if (isAsyncFunction(value)) type = `Async${type}`
    let base = `[${type}`
    if (constructor === null) base += ' (null prototype)'
    if (value.name === '') base += ' (anonymous)'
    else base += `: ${typeof value.name === 'string' ? value.name : formatValue(ctx, value.name)}`
    base += ']'
    if (constructor !== type && constructor !== null) base += ` ${constructor}`
    if (tag !== '' && constructor !== tag) base += ` [${tag}]`
    return base
  }

  function identicalSequenceRange(a, b) {
    for (let i = 0; i < a.length - 3; i++) {
      const pos = b.indexOf(a[i])
      if (pos !== -1) {
        const rest = b.length - pos
        if (rest > 3) {
          let len = 1
          const maxLen = Math.min(a.length - i, rest)
          while (maxLen > len && a[i + len] === b[pos + len]) len++
          if (len > 3) return [len, i]
        }
      }
    }
    return [0, 0]
  }

  function fixColumns(stack) {
    return stack
  }

  function getStackString(ctx, error) {
    let stack
    try {
      stack = error.stack
    } catch {
      // A stack getter that throws is ignored.
    }
    if (stack) {
      if (typeof stack === 'string') return fixColumns(stack)
      ctx.seen.push(error)
      ctx.indentationLvl += 4
      const result = formatValue(ctx, stack)
      ctx.indentationLvl -= 4
      ctx.seen.pop()
      return `${apply(errorToString, error, [])}\n    ${result}`
    }
    return apply(errorToString, error, [])
  }

  function getStackFrames(ctx, err, stack) {
    const frames = stack.split('\n')
    let cause
    try {
      ({ cause } = err)
    } catch {
      // A cause getter that throws is ignored.
    }
    // Frames identical to the cause's are left out.
    if (cause != null && isError(cause)) {
      const causeStack = getStackString(ctx, cause)
      const causeStackStart = causeStack.indexOf('\n    at')
      if (causeStackStart !== -1) {
        const causeFrames = causeStack.slice(causeStackStart + 1).split('\n')
        const [len, offset] = identicalSequenceRange(frames, causeFrames)
        if (len > 0) {
          const skipped = len - 2
          const msg = `    ... ${skipped} lines matching cause stack trace ...`
          frames.splice(offset + 1, skipped, ctx.stylize(msg, 'undefined'))
        }
      }
    }
    return frames
  }

  function improveStack(stack, constructor, name, tag) {
    let len = name.length
    if (typeof name !== 'string') {
      stack = stack.replace(`${name}`, `${name} [${getPrefix(constructor, tag, 'Error').slice(0, -1)}]`)
    }
    if (constructor === null || (name.endsWith('Error') && stack.startsWith(name) && (stack.length === len || stack[len] === ':' || stack[len] === '\n'))) {
      let fallback = 'Error'
      if (constructor === null) {
        const start = /^([A-Z][a-z_ A-Z0-9[\]()-]+)(?::|\n {4}at)/.exec(stack) || /^([a-z_A-Z0-9-]*Error)$/.exec(stack)
        fallback = (start?.[1]) || ''
        len = fallback.length
        fallback ||= 'Error'
      }
      const prefix = getPrefix(constructor, tag, fallback).slice(0, -1)
      if (name !== prefix) {
        if (prefix.includes(name)) {
          if (len === 0) stack = `${prefix}: ${stack}`
          else stack = `${prefix}${stack.slice(len)}`
        } else {
          stack = `${prefix} [${name}]${stack.slice(len)}`
        }
      }
    }
    return stack
  }

  function formatError(err, constructor, tag, ctx, keys) {
    let message
    let name
    let stack
    try {
      stack = getStackString(ctx, err)
    } catch {
      return apply(objectToString, err, [])
    }
    let messageIsGetterThatThrows = false
    try {
      message = err.message
    } catch {
      messageIsGetterThatThrows = true
    }
    let nameIsGetterThatThrows = false
    try {
      name = err.name
    } catch {
      nameIsGetterThatThrows = true
    }
    if (!ctx.showHidden && keys.length !== 0) {
      const index = keys.indexOf('stack')
      if (index !== -1) keys.splice(index, 1)
      // The message and the name are hidden when they are strings the stack holds.
      if (!messageIsGetterThatThrows) {
        const index = keys.indexOf('message')
        if (index !== -1 && (typeof message !== 'string' || stack.includes(message))) keys.splice(index, 1)
      }
      if (!nameIsGetterThatThrows) {
        const index = keys.indexOf('name')
        if (index !== -1 && (typeof name !== 'string' || stack.includes(name))) keys.splice(index, 1)
      }
    }
    name ??= 'Error'
    if (hasOwn(err, 'cause') && (keys.length === 0 || !keys.includes('cause'))) keys.push('cause')
    try {
      const errors = err.errors
      if (isArray(errors) && hasOwn(err, 'errors') && (keys.length === 0 || !keys.includes('errors'))) keys.push('errors')
    } catch {
      // An errors getter that throws is ignored.
    }
    stack = improveStack(stack, constructor, name, tag)
    // The message is not looked at when the stack holds it.
    let pos = (message && stack.indexOf(message)) || -1
    if (pos !== -1) pos += message.length
    // An error without a stack trace is wrapped in brackets.
    const stackStart = stack.indexOf('\n    at', pos)
    if (stackStart === -1) {
      stack = `[${stack}]`
    } else {
      const lines = getStackFrames(ctx, err, stack.slice(stackStart + 1))
      stack = `${stack.slice(0, stackStart)}\n${lines.join('\n')}`
    }
    // The message and the stack are indented as well.
    if (ctx.indentationLvl !== 0) {
      stack = stack.replaceAll('\n', `\n${' '.repeat(ctx.indentationLvl)}`)
    }
    return stack
  }

  function stripVTControlCharacters(str) {
    return str.replace(ansi, '')
  }

  function isFullWidthCodePoint(code) {
    return code >= 0x1100 && (
      code <= 0x115f ||
      code === 0x2329 ||
      code === 0x232a ||
      (code >= 0x2e80 && code <= 0x3247 && code !== 0x303f) ||
      (code >= 0x3250 && code <= 0x4dbf) ||
      (code >= 0x4e00 && code <= 0xa4c6) ||
      (code >= 0xa960 && code <= 0xa97c) ||
      (code >= 0xac00 && code <= 0xd7a3) ||
      (code >= 0xf900 && code <= 0xfaff) ||
      (code >= 0xfe10 && code <= 0xfe19) ||
      (code >= 0xfe30 && code <= 0xfe6b) ||
      (code >= 0xff01 && code <= 0xff60) ||
      (code >= 0xffe0 && code <= 0xffe6) ||
      (code >= 0x1b000 && code <= 0x1b001) ||
      (code >= 0x1f200 && code <= 0x1f251) ||
      (code >= 0x1f300 && code <= 0x1f64f) ||
      (code >= 0x20000 && code <= 0x3fffd))
  }

  function isZeroWidthCodePoint(code) {
    return code <= 0x1F ||
      (code >= 0x7F && code <= 0x9F) ||
      (code >= 0x300 && code <= 0x36F) ||
      (code >= 0x200B && code <= 0x200F) ||
      (code >= 0x20D0 && code <= 0x20FF) ||
      (code >= 0xFE00 && code <= 0xFE0F) ||
      (code >= 0xFE20 && code <= 0xFE2F) ||
      (code >= 0xE0100 && code <= 0xE01EF)
  }

  // The columns a string takes in a terminal, as Node measures them for grouping.
  function getStringWidth(str, removeControlChars = true) {
    if (removeControlChars) str = stripVTControlCharacters(str)
    let width = 0
    for (let i = 0; i < str.length; i++) {
      const code = str.charCodeAt(i)
      if (code >= 127) {
        for (const char of str.slice(i).normalize('NFC')) {
          const point = char.codePointAt(0)
          if (isFullWidthCodePoint(point)) width += 2
          else if (!isZeroWidthCodePoint(point)) width++
        }
        break
      }
      width += code >= 32 ? 1 : 0
    }
    return width
  }

  function groupArrayElements(ctx, output, value) {
    let totalLength = 0
    let maxLength = 0
    let i = 0
    let outputLength = output.length
    // The "... n more items" entry is left out.
    if (ctx.maxArrayLength < output.length) outputLength--
    const separatorSpace = 2
    const dataLen = new Array(outputLength)
    for (; i < outputLength; i++) {
      const len = getStringWidth(output[i], ctx.colors)
      dataLen[i] = len
      totalLength += len + separatorSpace
      if (maxLength < len) maxLength = len
    }
    const actualMax = maxLength + separatorSpace
    // At least three entries fit on a line, and no entry is much longer than the others.
    if (actualMax * 3 + ctx.indentationLvl < ctx.breakLength && (totalLength / actualMax > 5 || maxLength <= 6)) {
      const approxCharHeights = 2.5
      const averageBias = Math.sqrt(actualMax - totalLength / output.length)
      const biasedMax = Math.max(actualMax - 3 - averageBias, 1)
      const columns = Math.min(
        Math.round(Math.sqrt(approxCharHeights * biasedMax * outputLength) / biasedMax),
        Math.floor((ctx.breakLength - ctx.indentationLvl) / actualMax),
        ctx.compact * 4,
        15,
      )
      if (columns <= 1) return output
      const tmp = []
      const maxLineLength = []
      for (let i = 0; i < columns; i++) {
        let lineMaxLength = 0
        for (let j = i; j < output.length; j += columns) {
          if (dataLen[j] > lineMaxLength) lineMaxLength = dataLen[j]
        }
        maxLineLength.push(lineMaxLength + separatorSpace)
      }
      let padStart = true
      if (value !== undefined) {
        for (let i = 0; i < output.length; i++) {
          if (typeof value[i] !== 'number' && typeof value[i] !== 'bigint') {
            padStart = false
            break
          }
        }
      }
      for (let i = 0; i < outputLength; i += columns) {
        const max = Math.min(i + columns, outputLength)
        let str = ''
        let j = i
        for (; j < max - 1; j++) {
          const padding = maxLineLength[j - i] + output[j].length - dataLen[j]
          str += padStart ? `${output[j]}, `.padStart(padding, ' ') : `${output[j]}, `.padEnd(padding, ' ')
        }
        if (padStart) {
          const padding = maxLineLength[j - i] + output[j].length - dataLen[j] - separatorSpace
          str += output[j].padStart(padding, ' ')
        } else {
          str += output[j]
        }
        tmp.push(str)
      }
      if (ctx.maxArrayLength < output.length) tmp.push(output[outputLength])
      output = tmp
    }
    return output
  }

  function handleMaxCallStackSize(ctx, constructorName, indentationLvl) {
    ctx.seen.pop()
    ctx.indentationLvl = indentationLvl
    return ctx.stylize(`[${constructorName}: Inspection interrupted prematurely. Maximum call stack size exceeded.]`, 'special')
  }

  function addNumericSeparator(integerString) {
    let result = ''
    let i = integerString.length
    const start = integerString[0] === '-' ? 1 : 0
    for (; i >= start + 4; i -= 3) result = `_${integerString.slice(i - 3, i)}${result}`
    return i === integerString.length ? integerString : `${integerString.slice(0, i)}${result}`
  }

  function addNumericSeparatorEnd(integerString) {
    let result = ''
    let i = 0
    for (; i < integerString.length - 3; i += 3) result += `${integerString.slice(i, i + 3)}_`
    return i === 0 ? integerString : `${result}${integerString.slice(i)}`
  }

  const remainingText = remaining => `... ${remaining} more item${remaining > 1 ? 's' : ''}`

  function formatNumber(fn, number, numericSeparator) {
    if (!numericSeparator) {
      if (Object.is(number, -0)) return fn('-0', 'number')
      return fn(`${number}`, 'number')
    }
    const numberString = String(number)
    const integer = Math.trunc(number)
    if (integer === number) {
      if (!Number.isFinite(number) || numberString.includes('e')) return fn(numberString, 'number')
      return fn(addNumericSeparator(numberString), 'number')
    }
    if (Number.isNaN(number)) return fn(numberString, 'number')
    const decimalIndex = numberString.indexOf('.')
    return fn(`${addNumericSeparator(numberString.slice(0, decimalIndex))}.${addNumericSeparatorEnd(numberString.slice(decimalIndex + 1))}`, 'number')
  }

  function formatBigInt(fn, bigint, numericSeparator) {
    const string = String(bigint)
    if (!numericSeparator) return fn(`${string}n`, 'bigint')
    return fn(`${addNumericSeparator(string)}n`, 'bigint')
  }

  function formatPrimitive(fn, value, ctx) {
    if (typeof value === 'string') {
      let trailer = ''
      if (value.length > ctx.maxStringLength) {
        const remaining = value.length - ctx.maxStringLength
        value = value.slice(0, ctx.maxStringLength)
        trailer = `... ${remaining} more character${remaining > 1 ? 's' : ''}`
      }
      if (ctx.compact !== true && value.length > kMinLineLength && value.length > ctx.breakLength - ctx.indentationLvl - 4) {
        return value.split(/(?<=\n)/).map(line => fn(strEscape(line), 'string')).join(` +\n${' '.repeat(ctx.indentationLvl + 2)}`) + trailer
      }
      return fn(strEscape(value), 'string') + trailer
    }
    if (typeof value === 'number') return formatNumber(fn, value, ctx.numericSeparator)
    if (typeof value === 'bigint') return formatBigInt(fn, value, ctx.numericSeparator)
    if (typeof value === 'boolean') return fn(`${value}`, 'boolean')
    if (typeof value === 'undefined') return fn('undefined', 'undefined')
    return fn(apply(symbolToString, value, []), 'symbol')
  }

  function formatNamespaceObject(keys, ctx, value, recurseTimes) {
    const output = new Array(keys.length)
    for (let i = 0; i < keys.length; i++) {
      try {
        output[i] = formatProperty(ctx, value, recurseTimes, keys[i], kObjectType)
      } catch (err) {
        if (!(err instanceof ReferenceError)) throw err
        // A binding the module has not initialized yet.
        const tmp = { [keys[i]]: '' }
        output[i] = formatProperty(ctx, tmp, recurseTimes, keys[i], kObjectType)
        const pos = output[i].lastIndexOf(' ')
        output[i] = output[i].slice(0, pos + 1) + ctx.stylize('<uninitialized>', 'special')
      }
    }
    keys.length = 0
    return output
  }

  // The array is sparse and/or has extra keys.
  function formatSpecialArray(ctx, value, recurseTimes, maxLength, output, i) {
    const keys = objectKeys(value)
    let index = i
    for (; i < keys.length && output.length < maxLength; i++) {
      const key = keys[i]
      const tmp = +key
      if (tmp > 2 ** 32 - 2) break
      if (`${index}` !== key) {
        if (!numberRegExp.test(key)) break
        const emptyItems = tmp - index
        output.push(ctx.stylize(`<${emptyItems} empty item${emptyItems > 1 ? 's' : ''}>`, 'undefined'))
        index = tmp
        if (output.length === maxLength) break
      }
      output.push(formatProperty(ctx, value, recurseTimes, key, kArrayType))
      index++
    }
    const remaining = value.length - index
    if (output.length !== maxLength) {
      if (remaining > 0) output.push(ctx.stylize(`<${remaining} empty item${remaining > 1 ? 's' : ''}>`, 'undefined'))
    } else if (remaining > 0) {
      output.push(remainingText(remaining))
    }
    return output
  }

  function formatArrayBuffer(ctx, value) {
    let buffer
    try {
      buffer = new Uint8Array(value)
    } catch {
      return [ctx.stylize('(detached)', 'special')]
    }
    let hex = ''
    const shown = Math.min(ctx.maxArrayLength, buffer.length)
    for (let i = 0; i < shown; i++) hex += `${i === 0 ? '' : ' '}${buffer[i].toString(16).padStart(2, '0')}`
    const remaining = buffer.length - ctx.maxArrayLength
    if (remaining > 0) hex += ` ... ${remaining} more byte${remaining > 1 ? 's' : ''}`
    return [`${ctx.stylize('[Uint8Contents]', 'special')}: <${hex}>`]
  }

  function formatArray(ctx, value, recurseTimes) {
    const valLen = value.length
    const len = Math.min(Math.max(0, ctx.maxArrayLength), valLen)
    const remaining = valLen - len
    const output = []
    for (let i = 0; i < len; i++) {
      const desc = getOwnPropertyDescriptor(value, i)
      // Sparse arrays are handled apart.
      if (desc === undefined) return formatSpecialArray(ctx, value, recurseTimes, len, output, i)
      output.push(formatProperty(ctx, value, recurseTimes, i, kArrayType, desc))
    }
    if (remaining > 0) output.push(remainingText(remaining))
    return output
  }

  function formatTypedArray(value, length, ctx) {
    const maxLength = Math.min(Math.max(0, ctx.maxArrayLength), length)
    const remaining = value.length - maxLength
    const output = new Array(maxLength)
    const elementFormatter = value.length > 0 && typeof value[0] === 'number' ? formatNumber : formatBigInt
    for (let i = 0; i < maxLength; ++i) output[i] = elementFormatter(ctx.stylize, value[i], ctx.numericSeparator)
    if (remaining > 0) output[maxLength] = remainingText(remaining)
    return output
  }

  function formatExtraProperties(ctx, value, recurseTimes, key, typedArray) {
    ctx.indentationLvl += 2
    const str = formatValue(ctx, value[key], recurseTimes, typedArray)
    ctx.indentationLvl -= 2
    return `${ctx.stylize(`[${key}]`, 'string')}: ${str}`
  }

  function formatSet(values, length, ctx, recurseTimes) {
    const maxLength = Math.min(Math.max(0, ctx.maxArrayLength), length)
    const remaining = length - maxLength
    const output = []
    ctx.indentationLvl += 2
    let i = 0
    for (const v of values) {
      if (i >= maxLength) break
      output.push(formatValue(ctx, v, recurseTimes))
      i++
    }
    if (remaining > 0) output.push(remainingText(remaining))
    ctx.indentationLvl -= 2
    return output
  }

  function formatMap(entries, length, ctx, recurseTimes) {
    const maxLength = Math.min(Math.max(0, ctx.maxArrayLength), length)
    const remaining = length - maxLength
    const output = []
    ctx.indentationLvl += 2
    let i = 0
    for (const { 0: k, 1: v } of entries) {
      if (i >= maxLength) break
      output.push(`${formatValue(ctx, k, recurseTimes)} => ${formatValue(ctx, v, recurseTimes)}`)
      i++
    }
    if (remaining > 0) output.push(remainingText(remaining))
    ctx.indentationLvl -= 2
    return output
  }

  function formatWeakCollection(ctx) {
    return [ctx.stylize('<items unknown>', 'special')]
  }

  function formatPromise(ctx, value, recurseTimes) {
    const [state, result] = promiseDetails(value)
    if (state === 0) return [ctx.stylize('<pending>', 'special')]
    ctx.indentationLvl += 2
    const str = formatValue(ctx, result, recurseTimes)
    ctx.indentationLvl -= 2
    return [state === 2 ? `${ctx.stylize('<rejected>', 'special')} ${str}` : str]
  }

  function formatProperty(ctx, value, recurseTimes, key, type, desc, original = value) {
    let name
    let str
    let extra = ' '
    desc ??= getOwnPropertyDescriptor(value, key)
    if (desc.value !== undefined) {
      const diff = (ctx.compact !== true || type !== kObjectType) ? 2 : 3
      ctx.indentationLvl += diff
      str = formatValue(ctx, desc.value, recurseTimes)
      if (diff === 3 && ctx.breakLength < getStringWidth(str, ctx.colors)) {
        extra = `\n${' '.repeat(ctx.indentationLvl)}`
      }
      ctx.indentationLvl -= diff
    } else if (desc.get !== undefined) {
      const label = desc.set !== undefined ? 'Getter/Setter' : 'Getter'
      const s = ctx.stylize
      const sp = 'special'
      if (ctx.getters && (ctx.getters === true || (ctx.getters === 'get' && desc.set === undefined) || (ctx.getters === 'set' && desc.set !== undefined))) {
        try {
          const tmp = apply(desc.get, original, [])
          ctx.indentationLvl += 2
          if (tmp === null) str = `${s(`[${label}:`, sp)} ${s('null', 'null')}${s(']', sp)}`
          else if (typeof tmp === 'object') str = `${s(`[${label}]`, sp)} ${formatValue(ctx, tmp, recurseTimes)}`
          else str = `${s(`[${label}:`, sp)} ${formatPrimitive(s, tmp, ctx)}${s(']', sp)}`
          ctx.indentationLvl -= 2
        } catch (err) {
          str = `${s(`[${label}:`, sp)} <Inspection threw (${err.message})>${s(']', sp)}`
        }
      } else {
        str = ctx.stylize(`[${label}]`, sp)
      }
    } else if (desc.set !== undefined) {
      str = ctx.stylize('[Setter]', 'special')
    } else {
      str = ctx.stylize('undefined', 'undefined')
    }
    if (type === kArrayType) return str
    if (typeof key === 'symbol') {
      name = `[${ctx.stylize(apply(symbolToString, key, []).replace(strEscapeSequencesReplacer, escapeFn), 'symbol')}]`
    } else if (key === '__proto__') {
      name = "['__proto__']"
    } else if (desc.enumerable === false) {
      name = `[${String(key).replace(strEscapeSequencesReplacer, escapeFn)}]`
    } else if (keyStrRegExp.test(key)) {
      name = ctx.stylize(key, 'name')
    } else {
      name = ctx.stylize(strEscape(key), 'string')
    }
    return `${name}:${extra}${str}`
  }

  function isBelowBreakLength(ctx, output, start, base) {
    let totalLength = output.length + start
    if (totalLength + output.length > ctx.breakLength) return false
    for (let i = 0; i < output.length; i++) {
      totalLength += ctx.colors ? stripVTControlCharacters(output[i]).length : output[i].length
      if (totalLength > ctx.breakLength) return false
    }
    // Properties are not lined up on one line when base holds line breaks.
    return base === '' || !base.includes('\n')
  }

  function reduceToSingleString(ctx, output, base, braces, extrasType, recurseTimes, value) {
    if (ctx.compact !== true) {
      if (typeof ctx.compact === 'number' && ctx.compact >= 1) {
        const entries = output.length
        // Arrays with more than six entries are grouped in columns.
        if (extrasType === kArrayExtrasType && entries > 6) output = groupArrayElements(ctx, output, value)
        // The innermost ctx.compact levels go on one line when they fit.
        if (ctx.currentDepth - recurseTimes < ctx.compact && entries === output.length) {
          const start = output.length + ctx.indentationLvl + braces[0].length + base.length + 10
          if (isBelowBreakLength(ctx, output, start, base)) {
            const joinedOutput = output.join(', ')
            if (!joinedOutput.includes('\n')) {
              return `${base ? `${base} ` : ''}${braces[0]} ${joinedOutput}` + ` ${braces[1]}`
            }
          }
        }
      }
      const indentation = `\n${' '.repeat(ctx.indentationLvl)}`
      return `${base ? `${base} ` : ''}${braces[0]}${indentation}  ${output.join(`,${indentation}  `)}${indentation}${braces[1]}`
    }
    if (isBelowBreakLength(ctx, output, 0, base)) {
      return `${braces[0]}${base ? ` ${base}` : ''} ${output.join(', ')} ` + braces[1]
    }
    const indentation = ' '.repeat(ctx.indentationLvl)
    const ln = base === '' && braces[0].length === 1 ? ' ' : `${base ? ` ${base}` : ''}\n${indentation}  `
    return `${braces[0]}${ln}${output.join(`,\n${indentation}  `)} ${braces[1]}`
  }

  // Whether util.format's %s shows value with a toString of the language.
  function hasBuiltInToString(value) {
    let hasOwnToString = hasOwn
    let hasOwnToPrimitive = hasOwn
    if (typeof value.toString !== 'function') {
      if (typeof value[Symbol.toPrimitive] !== 'function') return true
      if (hasOwn(value, Symbol.toPrimitive)) return false
      hasOwnToString = () => false
    } else if (hasOwn(value, 'toString')) {
      return false
    } else if (typeof value[Symbol.toPrimitive] !== 'function') {
      hasOwnToPrimitive = () => false
    } else if (hasOwn(value, Symbol.toPrimitive)) {
      return false
    }
    let pointer = value
    do {
      pointer = getPrototypeOf(pointer)
    } while (!hasOwnToString(pointer, 'toString') && !hasOwnToPrimitive(pointer, Symbol.toPrimitive))
    const descriptor = getOwnPropertyDescriptor(pointer, 'constructor')
    return descriptor !== undefined && typeof descriptor.value === 'function' && builtInObjects.has(descriptor.value.name)
  }

  return { inspect, hasBuiltInToString }
})(host.promiseDetails, 0);
})
