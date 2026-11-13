package plugin

import (
	"crypto"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/hkdf"
	"crypto/md5"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha3"
	"crypto/sha512"
	"crypto/subtle"
	"crypto/x509"
	"encoding"
	"encoding/pem"
	"errors"
	"fmt"
	"hash"
	"math/big"
	"strings"

	"golang.org/x/crypto/chacha20poly1305"
	"golang.org/x/crypto/scrypt"

	"github.com/MoeForge/moejs"
	"github.com/MoeForge/moejs/engine"
)

// nodeHashes are the digests of node:crypto, by the names Node gives them.
var nodeHashes = map[string]func() hash.Hash{
	"md5":          md5.New,
	"sha1":         sha1.New,
	"sha224":       sha256.New224,
	"sha256":       sha256.New,
	"sha384":       sha512.New384,
	"sha512":       sha512.New,
	"sha512-224":   sha512.New512_224,
	"sha512-256":   sha512.New512_256,
	"sha3-224":     func() hash.Hash { return sha3.New224() },
	"sha3-256":     func() hash.Hash { return sha3.New256() },
	"sha3-384":     func() hash.Hash { return sha3.New384() },
	"sha3-512":     func() hash.Hash { return sha3.New512() },
	"rsa-md5":      md5.New,
	"rsa-sha1":     sha1.New,
	"rsa-sha224":   sha256.New224,
	"rsa-sha256":   sha256.New,
	"rsa-sha384":   sha512.New384,
	"rsa-sha512":   sha512.New,
	"sha-1":        sha1.New,
	"sha-256":      sha256.New,
	"sha-384":      sha512.New384,
	"sha-512":      sha512.New,
	"rsa-sha3-256": func() hash.Hash { return sha3.New256() },
}

// signatureHashes are the digests a signature names, as crypto.Hash.
var signatureHashes = map[string]crypto.Hash{
	"md5": crypto.MD5, "sha1": crypto.SHA1, "sha224": crypto.SHA224, "sha256": crypto.SHA256,
	"sha384": crypto.SHA384, "sha512": crypto.SHA512, "sha512-224": crypto.SHA512_224,
	"sha512-256": crypto.SHA512_256, "sha3-256": crypto.SHA3_256, "sha3-384": crypto.SHA3_384,
	"sha3-512": crypto.SHA3_512,
}

// hashNamed returns the digest of name, nil for an unknown one.
func hashNamed(name string) func() hash.Hash {
	return nodeHashes[strings.ToLower(name)]
}

// hashState is the state of a digest that can go to JavaScript and back.
func hashState(sum hash.Hash) ([]byte, error) {
	marshaler, ok := sum.(encoding.BinaryMarshaler)
	if !ok {
		return nil, errors.New("the digest cannot keep its state")
	}
	return marshaler.MarshalBinary()
}

// resumeHash is a digest of newHash at state.
func resumeHash(newHash func() hash.Hash, state []byte) (hash.Hash, error) {
	sum := newHash()
	unmarshaler, ok := sum.(encoding.BinaryUnmarshaler)
	if !ok {
		return nil, errors.New("the digest cannot keep its state")
	}
	if err := unmarshaler.UnmarshalBinary(state); err != nil {
		return nil, err
	}
	return sum, nil
}

// cryptoFunctions are the host's side of node:crypto. A Hash or Hmac keeps
// the state of its digest in JavaScript, so nothing outlives its object.
func (bound *instance) cryptoFunctions() map[string]any {
	digest := func(realm *moejs.Realm, args []moejs.Value) (func() hash.Hash, error) {
		name, err := stringArg(realm, args, 0, "algorithm")
		if err != nil {
			return nil, err
		}
		newHash := hashNamed(name)
		if newHash == nil {
			return nil, cryptoError(realm, "ERR_CRYPTO_INVALID_DIGEST", fmt.Sprintf("Invalid digest: %s", name))
		}
		return newHash, nil
	}
	bytesArg := func(realm *moejs.Realm, args []moejs.Value, index int, name string) ([]byte, error) {
		data, ok := bytesOf(moejs.Arg(args, index))
		if !ok {
			return nil, realm.TypeError("the %s must be bytes", name)
		}
		return data, nil
	}
	intArg := func(realm *moejs.Realm, args []moejs.Value, index int) (int, error) {
		number, err := realm.ToNumber(moejs.Arg(args, index))
		if err != nil {
			return 0, err
		}
		return int(number), nil
	}
	return map[string]any{
		"hashes": func() []any {
			names := make([]any, 0, len(nodeHashes))
			for name := range nodeHashes {
				names = append(names, name)
			}
			return names
		}(),
		"hashNew": bound.function("hashNew", 1, func(realm *moejs.Realm, _ moejs.Value, args []moejs.Value) (moejs.Value, error) {
			newHash, err := digest(realm, args)
			if err != nil {
				return moejs.Undefined(), err
			}
			state, err := hashState(newHash())
			if err != nil {
				return moejs.Undefined(), err
			}
			return bytesValue(realm, state)
		}),
		"hashUpdate": bound.function("hashUpdate", 3, func(realm *moejs.Realm, _ moejs.Value, args []moejs.Value) (moejs.Value, error) {
			newHash, err := digest(realm, args)
			if err != nil {
				return moejs.Undefined(), err
			}
			state, err := bytesArg(realm, args, 1, "state")
			if err != nil {
				return moejs.Undefined(), err
			}
			data, err := bytesArg(realm, args, 2, "data")
			if err != nil {
				return moejs.Undefined(), err
			}
			sum, err := resumeHash(newHash, state)
			if err != nil {
				return moejs.Undefined(), err
			}
			sum.Write(data)
			next, err := hashState(sum)
			if err != nil {
				return moejs.Undefined(), err
			}
			return bytesValue(realm, next)
		}),
		"hashDigest": bound.function("hashDigest", 2, func(realm *moejs.Realm, _ moejs.Value, args []moejs.Value) (moejs.Value, error) {
			newHash, err := digest(realm, args)
			if err != nil {
				return moejs.Undefined(), err
			}
			state, err := bytesArg(realm, args, 1, "state")
			if err != nil {
				return moejs.Undefined(), err
			}
			sum, err := resumeHash(newHash, state)
			if err != nil {
				return moejs.Undefined(), err
			}
			return bytesValue(realm, sum.Sum(nil))
		}),
		// hmacNew returns the state of the inner digest after the padded
		// key, and the outer pad that hmacDigest finishes with.
		"hmacNew": bound.function("hmacNew", 2, func(realm *moejs.Realm, _ moejs.Value, args []moejs.Value) (moejs.Value, error) {
			newHash, err := digest(realm, args)
			if err != nil {
				return moejs.Undefined(), err
			}
			key, err := bytesArg(realm, args, 1, "key")
			if err != nil {
				return moejs.Undefined(), err
			}
			inner := newHash()
			block := inner.BlockSize()
			if len(key) > block {
				keyHash := newHash()
				keyHash.Write(key)
				key = keyHash.Sum(nil)
			}
			ipad, opad := make([]byte, block), make([]byte, block)
			copy(ipad, key)
			copy(opad, key)
			for index := range ipad {
				ipad[index] ^= 0x36
				opad[index] ^= 0x5c
			}
			inner.Write(ipad)
			state, err := hashState(inner)
			if err != nil {
				return moejs.Undefined(), err
			}
			stateValue, err := bytesValue(realm, state)
			if err != nil {
				return moejs.Undefined(), err
			}
			padValue, err := bytesValue(realm, opad)
			if err != nil {
				return moejs.Undefined(), err
			}
			return engine.ObjectValue(realm.NewArray(stateValue, padValue)), nil
		}),
		"hmacDigest": bound.function("hmacDigest", 3, func(realm *moejs.Realm, _ moejs.Value, args []moejs.Value) (moejs.Value, error) {
			newHash, err := digest(realm, args)
			if err != nil {
				return moejs.Undefined(), err
			}
			state, err := bytesArg(realm, args, 1, "state")
			if err != nil {
				return moejs.Undefined(), err
			}
			opad, err := bytesArg(realm, args, 2, "pad")
			if err != nil {
				return moejs.Undefined(), err
			}
			inner, err := resumeHash(newHash, state)
			if err != nil {
				return moejs.Undefined(), err
			}
			outer := newHash()
			outer.Write(opad)
			outer.Write(inner.Sum(nil))
			return bytesValue(realm, outer.Sum(nil))
		}),
		"randomFill": bound.function("randomFill", 1, func(realm *moejs.Realm, _ moejs.Value, args []moejs.Value) (moejs.Value, error) {
			data, err := bytesArg(realm, args, 0, "buffer")
			if err != nil {
				return moejs.Undefined(), err
			}
			_, _ = rand.Read(data)
			return moejs.Undefined(), nil
		}),
		"timingSafeEqual": bound.function("timingSafeEqual", 2, func(realm *moejs.Realm, _ moejs.Value, args []moejs.Value) (moejs.Value, error) {
			a, err := bytesArg(realm, args, 0, "first buffer")
			if err != nil {
				return moejs.Undefined(), err
			}
			b, err := bytesArg(realm, args, 1, "second buffer")
			if err != nil {
				return moejs.Undefined(), err
			}
			return moejs.Bool(subtle.ConstantTimeCompare(a, b) == 1), nil
		}),
		"pbkdf2": bound.function("pbkdf2", 5, func(realm *moejs.Realm, _ moejs.Value, args []moejs.Value) (moejs.Value, error) {
			password, err := bytesArg(realm, args, 0, "password")
			if err != nil {
				return moejs.Undefined(), err
			}
			salt, err := bytesArg(realm, args, 1, "salt")
			if err != nil {
				return moejs.Undefined(), err
			}
			iterations, err := intArg(realm, args, 2)
			if err != nil {
				return moejs.Undefined(), err
			}
			length, err := intArg(realm, args, 3)
			if err != nil {
				return moejs.Undefined(), err
			}
			newHash, err := digest(realm, args[4:])
			if err != nil {
				return moejs.Undefined(), err
			}
			var key []byte
			bound.blocking(func() { key, err = pbkdf2.Key(newHash, string(password), salt, iterations, length) })
			if err != nil {
				return moejs.Undefined(), realm.RangeError("pbkdf2: %v", err)
			}
			return bytesValue(realm, key)
		}),
		"scrypt": bound.function("scrypt", 6, func(realm *moejs.Realm, _ moejs.Value, args []moejs.Value) (moejs.Value, error) {
			password, err := bytesArg(realm, args, 0, "password")
			if err != nil {
				return moejs.Undefined(), err
			}
			salt, err := bytesArg(realm, args, 1, "salt")
			if err != nil {
				return moejs.Undefined(), err
			}
			parameters := make([]int, 4)
			for index := range parameters {
				if parameters[index], err = intArg(realm, args, 2+index); err != nil {
					return moejs.Undefined(), err
				}
			}
			length, cost, blockSize, parallel := parameters[0], parameters[1], parameters[2], parameters[3]
			if 128*cost*blockSize > 64<<20 {
				return moejs.Undefined(), cryptoError(realm, "ERR_CRYPTO_INVALID_SCRYPT_PARAMS", "Invalid scrypt params: memory limit exceeded")
			}
			var key []byte
			bound.blocking(func() { key, err = scrypt.Key(password, salt, cost, blockSize, parallel, length) })
			if err != nil {
				return moejs.Undefined(), cryptoError(realm, "ERR_CRYPTO_INVALID_SCRYPT_PARAMS", "Invalid scrypt params: "+err.Error())
			}
			return bytesValue(realm, key)
		}),
		"hkdf": bound.function("hkdf", 5, func(realm *moejs.Realm, _ moejs.Value, args []moejs.Value) (moejs.Value, error) {
			newHash, err := digest(realm, args)
			if err != nil {
				return moejs.Undefined(), err
			}
			secret, err := bytesArg(realm, args, 1, "key")
			if err != nil {
				return moejs.Undefined(), err
			}
			salt, err := bytesArg(realm, args, 2, "salt")
			if err != nil {
				return moejs.Undefined(), err
			}
			info, err := bytesArg(realm, args, 3, "info")
			if err != nil {
				return moejs.Undefined(), err
			}
			length, err := intArg(realm, args, 4)
			if err != nil {
				return moejs.Undefined(), err
			}
			key, err := hkdf.Key(newHash, secret, salt, string(info), length)
			if err != nil {
				return moejs.Undefined(), cryptoError(realm, "ERR_CRYPTO_INVALID_KEYLEN", "Invalid key length")
			}
			return bytesValue(realm, key)
		}),
		"cipher": bound.function("cipher", 8, func(realm *moejs.Realm, _ moejs.Value, args []moejs.Value) (moejs.Value, error) {
			name, err := stringArg(realm, args, 0, "algorithm")
			if err != nil {
				return moejs.Undefined(), err
			}
			key, err := bytesArg(realm, args, 1, "key")
			if err != nil {
				return moejs.Undefined(), err
			}
			iv, _ := bytesOf(moejs.Arg(args, 2))
			data, err := bytesArg(realm, args, 3, "data")
			if err != nil {
				return moejs.Undefined(), err
			}
			encrypt := engine.ToBoolean(moejs.Arg(args, 4))
			aad, _ := bytesOf(moejs.Arg(args, 5))
			tag, _ := bytesOf(moejs.Arg(args, 6))
			padding := engine.ToBoolean(moejs.Arg(args, 7))
			output, outTag, err := runCipher(strings.ToLower(name), key, iv, data, encrypt, aad, tag, padding)
			if err != nil {
				var coded *codedError
				if errors.As(err, &coded) {
					return moejs.Undefined(), cryptoError(realm, coded.code, coded.message)
				}
				return moejs.Undefined(), err
			}
			outputValue, err := bytesValue(realm, output)
			if err != nil {
				return moejs.Undefined(), err
			}
			tagValue := moejs.Undefined()
			if outTag != nil {
				if tagValue, err = bytesValue(realm, outTag); err != nil {
					return moejs.Undefined(), err
				}
			}
			return engine.ObjectValue(realm.NewArray(outputValue, tagValue)), nil
		}),
		"keyInfo": bound.function("keyInfo", 2, func(realm *moejs.Realm, _ moejs.Value, args []moejs.Value) (moejs.Value, error) {
			text, err := stringArg(realm, args, 0, "key")
			if err != nil {
				return moejs.Undefined(), err
			}
			private := engine.ToBoolean(moejs.Arg(args, 1))
			key, err := parseKey(text, private)
			if err != nil {
				return moejs.Undefined(), cryptoError(realm, "ERR_INVALID_ARG_VALUE", err.Error())
			}
			info := map[string]any{"type": "public", "asymmetricKeyType": keyType(key)}
			if private {
				info["type"] = "private"
			}
			if public, err := publicPEM(key); err == nil {
				info["publicPem"] = public
			}
			switch key := key.(type) {
			case *rsa.PrivateKey:
				info["modulusLength"] = key.N.BitLen()
				info["publicExponent"] = key.E
			case *rsa.PublicKey:
				info["modulusLength"] = key.N.BitLen()
				info["publicExponent"] = key.E
			case *ecdsa.PrivateKey:
				info["namedCurve"] = curveName(key.Curve.Params().Name)
			case *ecdsa.PublicKey:
				info["namedCurve"] = curveName(key.Curve.Params().Name)
			}
			return realm.FromGo(info)
		}),
		"generateKeyPair": bound.function("generateKeyPair", 4, func(realm *moejs.Realm, _ moejs.Value, args []moejs.Value) (moejs.Value, error) {
			kind, err := stringArg(realm, args, 0, "type")
			if err != nil {
				return moejs.Undefined(), err
			}
			bits, err := intArg(realm, args, 1)
			if err != nil {
				return moejs.Undefined(), err
			}
			exponent, err := intArg(realm, args, 2)
			if err != nil {
				return moejs.Undefined(), err
			}
			curve, _ := stringArg(realm, args, 3, "namedCurve")
			var public, private string
			bound.blocking(func() { public, private, err = generateKeyPair(kind, bits, exponent, curve) })
			if err != nil {
				return moejs.Undefined(), cryptoError(realm, "ERR_INVALID_ARG_VALUE", err.Error())
			}
			return realm.FromGo([]any{public, private})
		}),
		"sign": bound.function("sign", 6, func(realm *moejs.Realm, _ moejs.Value, args []moejs.Value) (moejs.Value, error) {
			algorithm, key, data, options, err := signatureArgs(realm, args, true)
			if err != nil {
				return moejs.Undefined(), err
			}
			signature, err := signData(algorithm, key, data, options)
			if err != nil {
				return moejs.Undefined(), cryptoError(realm, "ERR_CRYPTO_SIGN_KEY_REQUIRED", err.Error())
			}
			return bytesValue(realm, signature)
		}),
		"verify": bound.function("verify", 7, func(realm *moejs.Realm, _ moejs.Value, args []moejs.Value) (moejs.Value, error) {
			algorithm, key, data, options, err := signatureArgs(realm, args, false)
			if err != nil {
				return moejs.Undefined(), err
			}
			signature, err := bytesArg(realm, args, 6, "signature")
			if err != nil {
				return moejs.Undefined(), err
			}
			return moejs.Bool(verifyData(algorithm, key, data, signature, options) == nil), nil
		}),
	}
}

// codedError is a failure of node:crypto with the code Node gives it.
type codedError struct{ code, message string }

func (failure *codedError) Error() string { return failure.message }

// cryptoError throws an Error with a code of Node.
func cryptoError(realm *moejs.Realm, code, message string) error {
	object := realm.NewError(engine.KindError, "%s", message)
	_, _ = object.CreateDataProperty(realm, realm.KeyFromGoString("code"), moejs.String(code))
	return realm.Throw(engine.ObjectValue(object))
}

// nodeCiphers are the ciphers createCipheriv knows, with their key length
// and IV length.
var nodeCiphers = map[string][2]int{
	"aes-128-cbc": {16, 16}, "aes-192-cbc": {24, 16}, "aes-256-cbc": {32, 16},
	"aes-128-ctr": {16, 16}, "aes-192-ctr": {24, 16}, "aes-256-ctr": {32, 16},
	"aes-128-gcm": {16, 12}, "aes-192-gcm": {24, 12}, "aes-256-gcm": {32, 12},
	"aes-128-ecb": {16, 0}, "aes-192-ecb": {24, 0}, "aes-256-ecb": {32, 0},
	"chacha20-poly1305": {32, 12},
}

// runCipher encrypts or decrypts data whole: node/crypto.js gathers what
// update receives and runs the cipher in final.
func runCipher(name string, key, iv, data []byte, encrypt bool, aad, tag []byte, padding bool) ([]byte, []byte, error) {
	sizes, known := nodeCiphers[name]
	if !known {
		return nil, nil, &codedError{"ERR_CRYPTO_UNKNOWN_CIPHER", "Unknown cipher"}
	}
	if len(key) != sizes[0] {
		return nil, nil, &codedError{"ERR_CRYPTO_INVALID_KEYLEN", "Invalid key length"}
	}
	mode := name[strings.LastIndex(name, "-")+1:]
	if name == "chacha20-poly1305" {
		mode = "poly1305"
	}
	if mode != "gcm" && mode != "poly1305" && sizes[1] != len(iv) {
		return nil, nil, &codedError{"ERR_CRYPTO_INVALID_IV", "Invalid initialization vector"}
	}
	var aead cipher.AEAD
	var block cipher.Block
	var err error
	switch mode {
	case "poly1305":
		aead, err = chacha20poly1305.New(key)
	default:
		block, err = aes.NewCipher(key)
		if err == nil && mode == "gcm" {
			aead, err = cipher.NewGCMWithNonceSize(block, len(iv))
		}
	}
	if err != nil {
		return nil, nil, &codedError{"ERR_CRYPTO_INVALID_IV", err.Error()}
	}
	switch mode {
	case "gcm", "poly1305":
		if len(iv) != aead.NonceSize() && mode == "poly1305" {
			return nil, nil, &codedError{"ERR_CRYPTO_INVALID_IV", "Invalid initialization vector"}
		}
		if encrypt {
			sealed := aead.Seal(nil, iv, data, aad)
			cut := len(sealed) - aead.Overhead()
			return sealed[:cut], sealed[cut:], nil
		}
		if len(tag) == 0 {
			return nil, nil, &codedError{"ERR_CRYPTO_INVALID_STATE", "Unsupported state or unable to authenticate data"}
		}
		opened, err := aead.Open(nil, iv, append(append([]byte{}, data...), tag...), aad)
		if err != nil {
			return nil, nil, &codedError{"ERR_CRYPTO_INVALID_STATE", "Unsupported state or unable to authenticate data"}
		}
		return opened, nil, nil
	case "ctr":
		output := make([]byte, len(data))
		cipher.NewCTR(block, iv).XORKeyStream(output, data)
		return output, nil, nil
	}
	size := block.BlockSize()
	if encrypt && padding {
		pad := size - len(data)%size
		data = append(append([]byte{}, data...), make([]byte, pad)...)
		for index := len(data) - pad; index < len(data); index++ {
			data[index] = byte(pad)
		}
	}
	if len(data)%size != 0 {
		if encrypt {
			return nil, nil, &codedError{"ERR_CRYPTO_INVALID_STATE", "data not multiple of block length"}
		}
		return nil, nil, &codedError{"ERR_OSSL_WRONG_FINAL_BLOCK_LENGTH", "wrong final block length"}
	}
	output := make([]byte, len(data))
	switch mode {
	case "cbc":
		if encrypt {
			cipher.NewCBCEncrypter(block, iv).CryptBlocks(output, data)
		} else {
			cipher.NewCBCDecrypter(block, iv).CryptBlocks(output, data)
		}
	case "ecb":
		for offset := 0; offset < len(data); offset += size {
			if encrypt {
				block.Encrypt(output[offset:offset+size], data[offset:offset+size])
			} else {
				block.Decrypt(output[offset:offset+size], data[offset:offset+size])
			}
		}
	}
	if !encrypt && padding {
		if len(output) == 0 {
			return nil, nil, &codedError{"ERR_OSSL_BAD_DECRYPT", "bad decrypt"}
		}
		pad := int(output[len(output)-1])
		if pad == 0 || pad > size || pad > len(output) {
			return nil, nil, &codedError{"ERR_OSSL_BAD_DECRYPT", "bad decrypt"}
		}
		for _, value := range output[len(output)-pad:] {
			if int(value) != pad {
				return nil, nil, &codedError{"ERR_OSSL_BAD_DECRYPT", "bad decrypt"}
			}
		}
		output = output[:len(output)-pad]
	}
	return output, nil, nil
}

// generateKeyPair makes a key pair of kind rsa, ec or ed25519, as SPKI and
// PKCS #8 PEM.
func generateKeyPair(kind string, bits, exponent int, curve string) (string, string, error) {
	var private crypto.Signer
	var err error
	switch kind {
	case "rsa":
		if exponent != 65537 {
			return "", "", errors.New("RSA keys are made with the public exponent 65537")
		}
		if bits < 1024 || bits > 8192 {
			return "", "", errors.New("the modulus length of an RSA key is 1024 to 8192 bits")
		}
		private, err = rsa.GenerateKey(rand.Reader, bits)
	case "ec":
		var named elliptic.Curve
		switch curve {
		case "prime256v1", "P-256", "secp256r1":
			named = elliptic.P256()
		case "secp384r1", "P-384":
			named = elliptic.P384()
		case "secp521r1", "P-521":
			named = elliptic.P521()
		default:
			return "", "", fmt.Errorf("the curve %q is not one of prime256v1, secp384r1 and secp521r1", curve)
		}
		private, err = ecdsa.GenerateKey(named, rand.Reader)
	case "ed25519":
		_, private, err = ed25519.GenerateKey(rand.Reader)
	default:
		return "", "", fmt.Errorf("the key type %q is not one of rsa, ec and ed25519", kind)
	}
	if err != nil {
		return "", "", err
	}
	privateDER, err := x509.MarshalPKCS8PrivateKey(private)
	if err != nil {
		return "", "", err
	}
	public, err := publicPEM(private)
	if err != nil {
		return "", "", err
	}
	return public, string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateDER})), nil
}

// parseKey reads a key in PEM: a private key in PKCS #8, PKCS #1 or SEC 1,
// or, unless private is set, a public key in PKIX or PKCS #1, or a
// certificate. A private key also serves where a public one is wanted.
func parseKey(text string, private bool) (any, error) {
	rest := []byte(text)
	for {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			return nil, errors.New("the key is not in PEM, or of a kind node:crypto here does not read")
		}
		switch block.Type {
		case "PRIVATE KEY":
			return x509.ParsePKCS8PrivateKey(block.Bytes)
		case "RSA PRIVATE KEY":
			return x509.ParsePKCS1PrivateKey(block.Bytes)
		case "EC PRIVATE KEY":
			return x509.ParseECPrivateKey(block.Bytes)
		}
		if private {
			continue
		}
		switch block.Type {
		case "PUBLIC KEY":
			return x509.ParsePKIXPublicKey(block.Bytes)
		case "RSA PUBLIC KEY":
			return x509.ParsePKCS1PublicKey(block.Bytes)
		case "CERTIFICATE":
			certificate, err := x509.ParseCertificate(block.Bytes)
			if err != nil {
				return nil, err
			}
			return certificate.PublicKey, nil
		}
	}
}

// keyType is a key's asymmetricKeyType.
func keyType(key any) string {
	switch key.(type) {
	case *rsa.PrivateKey, *rsa.PublicKey:
		return "rsa"
	case *ecdsa.PrivateKey, *ecdsa.PublicKey:
		return "ec"
	case ed25519.PrivateKey, ed25519.PublicKey:
		return "ed25519"
	}
	return ""
}

// curveName is Node's name of a curve.
func curveName(name string) string {
	switch name {
	case "P-256":
		return "prime256v1"
	case "P-384":
		return "secp384r1"
	case "P-521":
		return "secp521r1"
	}
	return name
}

// publicPEM is the public half of a key in PKIX PEM.
func publicPEM(key any) (string, error) {
	if signer, ok := key.(crypto.Signer); ok {
		key = signer.Public()
	}
	der, err := x509.MarshalPKIXPublicKey(key)
	if err != nil {
		return "", err
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})), nil
}

// signatureOptions are the options of sign and verify beside the key.
type signatureOptions struct {
	pss        bool
	saltLength int
	ieeeP1363  bool
}

// signatureArgs reads (algorithm, key, private, data, padding, saltLength,
// dsaEncoding) of sign, and of verify before the signature.
func signatureArgs(realm *moejs.Realm, args []moejs.Value, private bool) (string, any, []byte, signatureOptions, error) {
	var options signatureOptions
	algorithm := ""
	if value := moejs.Arg(args, 0); !value.IsNull() && !value.IsUndefined() {
		name, err := stringArg(realm, args, 0, "algorithm")
		if err != nil {
			return "", nil, nil, options, err
		}
		algorithm = strings.TrimPrefix(strings.ToLower(name), "rsa-")
	}
	text, err := stringArg(realm, args, 1, "key")
	if err != nil {
		return "", nil, nil, options, err
	}
	key, err := parseKey(text, private)
	if err != nil {
		return "", nil, nil, options, cryptoError(realm, "ERR_INVALID_ARG_VALUE", err.Error())
	}
	data, ok := bytesOf(moejs.Arg(args, 2))
	if !ok {
		return "", nil, nil, options, realm.TypeError("the data must be bytes")
	}
	if padding, err := realm.ToNumber(moejs.Arg(args, 3)); err == nil && padding == 6 {
		options.pss = true
	}
	options.saltLength = rsa.PSSSaltLengthEqualsHash
	if value := moejs.Arg(args, 4); !value.IsUndefined() && !value.IsNull() {
		if length, err := realm.ToNumber(value); err == nil {
			switch int(length) {
			case -1:
				options.saltLength = rsa.PSSSaltLengthEqualsHash
			case -2:
				options.saltLength = rsa.PSSSaltLengthAuto
			default:
				options.saltLength = int(length)
			}
		}
	}
	if encoding, err := realm.ToString(moejs.Arg(args, 5)); err == nil && encoding.String() == "ieee-p1363" {
		options.ieeeP1363 = true
	}
	return algorithm, key, data, options, nil
}

// signData signs data with key.
func signData(algorithm string, key any, data []byte, options signatureOptions) ([]byte, error) {
	if private, ok := key.(ed25519.PrivateKey); ok {
		return ed25519.Sign(private, data), nil
	}
	if algorithm == "" {
		algorithm = "sha256"
	}
	digest, err := signatureDigest(algorithm, data)
	if err != nil {
		return nil, err
	}
	switch key := key.(type) {
	case *rsa.PrivateKey:
		if options.pss {
			return rsa.SignPSS(rand.Reader, key, signatureHashes[algorithm], digest, &rsa.PSSOptions{SaltLength: options.saltLength})
		}
		return rsa.SignPKCS1v15(rand.Reader, key, signatureHashes[algorithm], digest)
	case *ecdsa.PrivateKey:
		if !options.ieeeP1363 {
			return ecdsa.SignASN1(rand.Reader, key, digest)
		}
		r, s, err := ecdsa.Sign(rand.Reader, key, digest)
		if err != nil {
			return nil, err
		}
		size := (key.Curve.Params().BitSize + 7) / 8
		signature := make([]byte, 2*size)
		r.FillBytes(signature[:size])
		s.FillBytes(signature[size:])
		return signature, nil
	}
	return nil, errors.New("the key cannot sign")
}

// verifyData checks signature of data with key, nil when it holds.
func verifyData(algorithm string, key any, data, signature []byte, options signatureOptions) error {
	if signer, ok := key.(crypto.Signer); ok {
		key = signer.Public()
	}
	if public, ok := key.(ed25519.PublicKey); ok {
		if !ed25519.Verify(public, data, signature) {
			return errors.New("invalid signature")
		}
		return nil
	}
	if algorithm == "" {
		algorithm = "sha256"
	}
	digest, err := signatureDigest(algorithm, data)
	if err != nil {
		return err
	}
	switch key := key.(type) {
	case *rsa.PublicKey:
		if options.pss {
			return rsa.VerifyPSS(key, signatureHashes[algorithm], digest, signature, &rsa.PSSOptions{SaltLength: options.saltLength})
		}
		return rsa.VerifyPKCS1v15(key, signatureHashes[algorithm], digest, signature)
	case *ecdsa.PublicKey:
		if !options.ieeeP1363 {
			if !ecdsa.VerifyASN1(key, digest, signature) {
				return errors.New("invalid signature")
			}
			return nil
		}
		half := len(signature) / 2
		r, s := new(big.Int).SetBytes(signature[:half]), new(big.Int).SetBytes(signature[half:])
		if !ecdsa.Verify(key, digest, r, s) {
			return errors.New("invalid signature")
		}
		return nil
	}
	return errors.New("the key cannot verify")
}

// signatureDigest is the digest of data a signature with algorithm covers.
func signatureDigest(algorithm string, data []byte) ([]byte, error) {
	kind, ok := signatureHashes[algorithm]
	if !ok || !kind.Available() {
		return nil, fmt.Errorf("Invalid digest: %s", algorithm)
	}
	sum := kind.New()
	sum.Write(data)
	return sum.Sum(nil), nil
}
