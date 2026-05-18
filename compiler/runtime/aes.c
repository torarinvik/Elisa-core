#include <stdint.h>
#include <string.h>

#if defined(__APPLE__)
#include <CommonCrypto/CommonCryptor.h>
#endif

static int elisacore_aes_key_bits(int64_t key_size) {
    switch (key_size) {
    case 16:
        return 128;
    case 24:
        return 192;
    case 32:
        return 256;
    default:
        return 0;
    }
}

void aes_encrypt_block(uint8_t* key, int64_t key_size, uint8_t* input, uint8_t* output) {
    if (key == 0 || input == 0 || output == 0) {
        return;
    }
#if defined(__APPLE__)
    const int bits = elisacore_aes_key_bits(key_size);
    if (bits == 0) {
        memset(output, 0, 16);
        return;
    }
    size_t moved = 0;
    CCCrypt(kCCEncrypt, kCCAlgorithmAES, kCCOptionECBMode, key, (size_t)key_size, 0, input, 16,
            output, 16, &moved);
#else
    (void)key;
    (void)key_size;
    memcpy(output, input, 16);
#endif
}

void aes_decrypt_block(uint8_t* key, int64_t key_size, uint8_t* input, uint8_t* output) {
    if (key == 0 || input == 0 || output == 0) {
        return;
    }
#if defined(__APPLE__)
    const int bits = elisacore_aes_key_bits(key_size);
    if (bits == 0) {
        memset(output, 0, 16);
        return;
    }
    size_t moved = 0;
    CCCrypt(kCCDecrypt, kCCAlgorithmAES, kCCOptionECBMode, key, (size_t)key_size, 0, input, 16,
            output, 16, &moved);
#else
    (void)key;
    (void)key_size;
    memcpy(output, input, 16);
#endif
}

void aes_cbc_encrypt(uint8_t* key, int64_t key_size, uint8_t* iv, uint8_t* input,
                     int64_t input_size, uint8_t* output) {
    if (key == 0 || iv == 0 || input == 0 || output == 0 || input_size < 0) {
        return;
    }
#if defined(__APPLE__)
    if (elisacore_aes_key_bits(key_size) == 0 || (input_size % 16) != 0) {
        memset(output, 0, (size_t)input_size);
        return;
    }
    uint8_t iv_copy[16];
    memcpy(iv_copy, iv, 16);
    size_t moved = 0;
    CCCrypt(kCCEncrypt, kCCAlgorithmAES, 0, key, (size_t)key_size, iv_copy, input,
            (size_t)input_size, output, (size_t)input_size, &moved);
#else
    (void)key;
    (void)key_size;
    (void)iv;
    memcpy(output, input, (size_t)input_size);
#endif
}

void aes_cbc_decrypt(uint8_t* key, int64_t key_size, uint8_t* iv, uint8_t* input,
                     int64_t input_size, uint8_t* output) {
    if (key == 0 || iv == 0 || input == 0 || output == 0 || input_size < 0) {
        return;
    }
#if defined(__APPLE__)
    if (elisacore_aes_key_bits(key_size) == 0 || (input_size % 16) != 0) {
        memset(output, 0, (size_t)input_size);
        return;
    }
    uint8_t iv_copy[16];
    memcpy(iv_copy, iv, 16);
    size_t moved = 0;
    CCCrypt(kCCDecrypt, kCCAlgorithmAES, 0, key, (size_t)key_size, iv_copy, input,
            (size_t)input_size, output, (size_t)input_size, &moved);
#else
    (void)key;
    (void)key_size;
    (void)iv;
    memcpy(output, input, (size_t)input_size);
#endif
}
