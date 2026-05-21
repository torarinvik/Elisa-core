// SPDX-FileCopyrightText: Copyright 2026 shadPS4 Emulator Project
// SPDX-License-Identifier: GPL-2.0-or-later

/*
 * Minimal AES runtime translation unit.
 *
 * Some CLI test targets expect compiler/runtime/aes.c to exist in the default
 * runtime file set even when no AES symbols are referenced by the program.
 */
void elisa_runtime_aes_placeholder(void) {}
