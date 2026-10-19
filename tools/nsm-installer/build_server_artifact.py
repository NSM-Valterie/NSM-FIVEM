#!/usr/bin/env python3
"""Build and patch the ready-to-run NSM Dedicated Server artifact (NSM-Server.zip)
from NSM-server-v0.8.1-localtest.zip + cfx-server-data resources.
"""
import hashlib
import json
import re
import struct
import sys
import zipfile
from datetime import datetime, timezone
from pathlib import Path


def sha(b: bytes) -> str:
    return hashlib.sha256(b).hexdigest()


def parse_pe_sections(data: bytearray):
    if len(data) < 0x100 or data[:2] != b"MZ":
        return []
    pe_off = struct.unpack_from("<I", data, 0x3C)[0]
    if pe_off + 24 > len(data) or data[pe_off : pe_off + 4] != b"PE\x00\x00":
        return []
    num_sec = struct.unpack_from("<H", data, pe_off + 6)[0]
    opt_size = struct.unpack_from("<H", data, pe_off + 20)[0]
    sec_table = pe_off + 24 + opt_size
    sections = []
    for i in range(num_sec):
        s_off = sec_table + i * 40
        if s_off + 40 > len(data):
            break
        s_name = data[s_off : s_off + 8].rstrip(b"\x00").decode("ascii", "ignore")
        vsize, rva, raw_size, raw_ptr = struct.unpack_from("<IIII", data, s_off + 8)
        sections.append([s_off, s_name, vsize, rva, raw_size, raw_ptr])
    return sections


def file_off_to_rva(sections, foff: int):
    for _, _, vsize, rva, raw_size, raw_ptr in sections:
        if raw_ptr <= foff < raw_ptr + raw_size:
            return rva + (foff - raw_ptr)
    return None


def alloc_in_rdata_cave(data: bytearray, sections, payload: bytes):
    # 1. Try standard section tail padding in .rdata / .data / .text
    for sec in sections:
        s_off, s_name, vsize, rva, raw_size, raw_ptr = sec
        if s_name in (".rdata", ".data", ".text") and vsize < raw_size:
            cave_off = (vsize + 7) & ~7
            if cave_off + len(payload) <= raw_size:
                cave_raw = raw_ptr + cave_off
                new_rva = rva + cave_off
                data[cave_raw : cave_raw + len(payload)] = payload
                sec[2] = cave_off + len(payload)
                struct.pack_into("<I", data, s_off + 8, sec[2])
                return new_rva

    # 2. Try reclaiming donor error-report tails inside .rdata ("Please report this error at ...")
    donor_tail = b"Please report this error at https://github.com/citizenfx/fivem.\n\x00"
    d_idx = data.find(donor_tail)
    if d_idx != -1 and len(payload) + 2 <= len(donor_tail):
        data[d_idx : d_idx + 2] = b"\n\x00"
        cave_raw = d_idx + 2
        data[cave_raw : cave_raw + len(donor_tail) - 2] = b"\x00" * (len(donor_tail) - 2)
        data[cave_raw : cave_raw + len(payload)] = payload
        return file_off_to_rva(sections, cave_raw)

    # 3. Try any contiguous run of >= len(payload)+16 zero bytes at the end of .rdata raw data
    for sec in sections:
        s_off, s_name, vsize, rva, raw_size, raw_ptr = sec
        if s_name == ".rdata" and raw_size > len(payload) + 32:
            tail_start = raw_ptr + raw_size - len(payload) - 8
            if all(b == 0 for b in data[tail_start - 8 : raw_ptr + raw_size]):
                data[tail_start : tail_start + len(payload)] = payload
                used_end = (tail_start - raw_ptr) + len(payload)
                if used_end > vsize:
                    sec[2] = used_end
                    struct.pack_into("<I", data, s_off + 8, used_end)
                return rva + (tail_start - raw_ptr)

    return None


def patch_nearby_ascii_lengths(
    data: bytearray, lea_pos: int, sec_start: int, sec_end: int, old_len: int, new_len: int
) -> int:
    if old_len == new_len or old_len <= 4:
        return 0
    old_u32 = struct.pack("<I", old_len)
    new_u32 = struct.pack("<I", new_len)
    win_start = max(sec_start, lea_pos - 48)
    win_end = min(sec_end - 12, lea_pos + 56)
    patched = 0
    k = win_start
    while k < win_end:
        if data[k : k + 4] == old_u32:
            is_mov_reg = (k >= 1 and 0xB8 <= data[k - 1] <= 0xBF) or (
                k >= 2 and data[k - 2] in (0x41, 0x48, 0x49) and 0xB8 <= data[k - 1] <= 0xBF
            )
            is_mov_c7 = (
                (k >= 2 and data[k - 2] == 0xC7 and 0xC0 <= data[k - 1] <= 0xC7)
                or (k >= 3 and data[k - 3] in (0x48, 0x49) and data[k - 2] == 0xC7 and 0xC0 <= data[k - 1] <= 0xC7)
                or (k >= 3 and data[k - 3] == 0xC7 and (data[k - 2] & 0x38) == 0x00)
                or (k >= 4 and data[k - 4] == 0xC7 and (data[k - 3] & 0x38) == 0x00)
                or (k >= 5 and data[k - 5] in (0x48, 0x49) and data[k - 4] == 0xC7 and (data[k - 3] & 0x38) == 0x00)
                or (k >= 6 and data[k - 6] == 0xC7 and (data[k - 5] & 0x38) == 0x00)
                or (k >= 7 and data[k - 7] == 0xC7 and (data[k - 6] & 0x38) == 0x00)
                or (k >= 8 and data[k - 8] in (0x48, 0x49) and data[k - 7] == 0xC7 and (data[k - 6] & 0x38) == 0x00)
            )
            if is_mov_reg or is_mov_c7:
                data[k : k + 4] = new_u32
                patched += 1
                k += 4
                continue
        if old_len < 128 and new_len < 128:
            if (
                data[k] in (0x48, 0x49)
                and data[k + 1] == 0x83
                and 0x78 <= data[k + 2] <= 0x7F
                and data[k + 2] != 0x7C
                and data[k + 4] == old_len
            ):
                data[k + 4] = new_len
                patched += 1
                k += 5
                continue
            if (
                data[k] in (0x48, 0x49)
                and data[k + 1] == 0x83
                and data[k + 2] == 0x7C
                and data[k + 3] == 0x24
                and data[k + 5] == old_len
            ):
                data[k + 5] = new_len
                patched += 1
                k += 6
                continue
            if (
                data[k] in (0x48, 0x49)
                and data[k + 1] == 0x83
                and 0xF8 <= data[k + 2] <= 0xFF
                and data[k + 3] == old_len
            ):
                data[k + 3] = new_len
                patched += 1
                k += 4
                continue
        k += 1
    return patched


def redirect_ascii_string(data: bytearray, sections, old_bytes: bytes, new_bytes: bytes, label: str) -> int:
    if old_bytes == new_bytes:
        return 0
    needle = old_bytes + b"\x00"
    patched = 0
    pos = 0
    while True:
        idx = data.find(needle, pos)
        if idx == -1:
            break
        pos = idx + 1
        old_rva = file_off_to_rva(sections, idx)
        if len(new_bytes) <= len(old_bytes):
            if old_rva is not None and len(new_bytes) != len(old_bytes):
                for _, t_name, t_vsize, t_rva, t_raw_size, t_raw_ptr in sections:
                    if t_name in (".text", ".zdata", ".ctext"):
                        t_end = t_raw_ptr + min(t_vsize, t_raw_size)
                        for p in range(t_raw_ptr, t_end - 7):
                            if data[p] in (0x48, 0x4C) and data[p + 1] == 0x8D and (data[p + 2] & 0xC7) == 0x05:
                                disp = struct.unpack_from("<i", data, p + 3)[0]
                                if (t_rva + (p - t_raw_ptr) + 7 + disp) == old_rva:
                                    patch_nearby_ascii_lengths(
                                        data, p, t_raw_ptr, t_end, len(old_bytes), len(new_bytes)
                                    )
            data[idx : idx + len(old_bytes) + 1] = (new_bytes + b"\x00").ljust(len(old_bytes) + 1, b"\x00")
            patched += 1
            print(f"[server-pe] {label}: in-place {old_bytes!r} -> {new_bytes!r} at 0x{idx:x}")
        elif old_rva is not None:
            new_rva = alloc_in_rdata_cave(data, sections, new_bytes + b"\x00")
            assert new_rva is not None, f"Failed to allocate .rdata cave for {new_bytes!r} in {label}"
            lea_count = 0
            len_count = 0
            for _, t_name, t_vsize, t_rva, t_raw_size, t_raw_ptr in sections:
                if t_name in (".text", ".zdata", ".ctext"):
                    t_end = t_raw_ptr + min(t_vsize, t_raw_size)
                    for p in range(t_raw_ptr, t_end - 7):
                        if data[p] in (0x48, 0x4C) and data[p + 1] == 0x8D and (data[p + 2] & 0xC7) == 0x05:
                            disp = struct.unpack_from("<i", data, p + 3)[0]
                            insn_end_rva = t_rva + (p - t_raw_ptr) + 7
                            if insn_end_rva + disp == old_rva:
                                struct.pack_into("<i", data, p + 3, new_rva - insn_end_rva)
                                lea_count += 1
                                len_count += patch_nearby_ascii_lengths(
                                    data, p, t_raw_ptr, t_end, len(old_bytes), len(new_bytes)
                                )
                        elif data[p] == 0x8D and (data[p + 1] & 0xC7) == 0x05:
                            disp = struct.unpack_from("<i", data, p + 2)[0]
                            insn_end_rva = t_rva + (p - t_raw_ptr) + 6
                            if insn_end_rva + disp == old_rva:
                                struct.pack_into("<i", data, p + 2, new_rva - insn_end_rva)
                                lea_count += 1
                                len_count += patch_nearby_ascii_lengths(
                                    data, p, t_raw_ptr, t_end, len(old_bytes), len(new_bytes)
                                )
            data[idx : idx + len(needle)] = b"\x00" * len(needle)
            patched += 1
            print(
                f"[server-pe] {label}: cave-redirected {old_bytes!r} -> {new_bytes!r} "
                f"(0x{old_rva:x} -> 0x{new_rva:x}, {lea_count} LEAs, {len_count} len immediates)"
            )
    return patched


def patch_utf16_with_len(data: bytearray, sections, old_s: str, new_s: str) -> None:
    assert len(new_s) <= len(old_s)
    old_b = old_s.encode("utf-16le") + b"\x00\x00"
    new_b = new_s.encode("utf-16le") + b"\x00\x00" * (len(old_s) - len(new_s) + 1)
    old_len = len(old_s)
    new_len = len(new_s)
    old_imm = struct.pack("<I", old_len)
    new_imm = struct.pack("<I", new_len)
    p_idx = 0
    while True:
        idx = data.find(old_b, p_idx)
        if idx < 0:
            break
        if sections and old_len != new_len:
            target_rva = file_off_to_rva(sections, idx)
            if target_rva is not None:
                for _, s_name, _, t_rva, t_raw_size, t_raw_ptr in sections:
                    if s_name in (".text", ".zdata", ".ctext"):
                        for p in range(t_raw_ptr, t_raw_ptr + t_raw_size - 7):
                            if data[p] in (0x48, 0x4C) and data[p + 1] == 0x8D and (data[p + 2] & 0xC7) == 0x05:
                                disp = struct.unpack_from("<i", data, p + 3)[0]
                                if (t_rva + (p - t_raw_ptr) + 7 + disp) == target_rva:
                                    w_start = max(t_raw_ptr, p - 48)
                                    w_end = min(t_raw_ptr + t_raw_size - 4, p + 48)
                                    for q in range(w_start, w_end):
                                        if data[q : q + 4] == old_imm:
                                            is_mov_reg = q >= 1 and 0xB8 <= data[q - 1] <= 0xBF
                                            is_mov_mem = (
                                                (q >= 3 and data[q - 3] == 0xC7 and (data[q - 2] & 0x38) == 0x00)
                                                or (q >= 4 and data[q - 4] == 0xC7 and (data[q - 3] & 0x38) == 0x00)
                                                or (q >= 6 and data[q - 6] == 0xC7 and (data[q - 5] & 0x38) == 0x00)
                                                or (q >= 7 and data[q - 7] == 0xC7 and (data[q - 6] & 0x38) == 0x00)
                                            )
                                            if is_mov_reg or is_mov_mem:
                                                data[q : q + 4] = new_imm
        data[idx : idx + len(old_b)] = new_b
        p_idx = idx + len(new_b)


def rename_server_member(rel: str) -> str:
    rel = rel.replace("\\", "/").lstrip("/")
    if rel.lower().startswith("citizen/"):
        rel = "AxiomFX/" + rel[len("citizen/") :]
    if "/" not in rel:
        low = rel.lower()
        if low == "fxserver.exe":
            return "nsm-server.exe"
        if low.startswith("citizen-") and low.endswith(".dll"):
            return "AxiomFX-" + rel[len("citizen-") :]
        if low == "citizengame.dll":
            return "AxiomFXGame.dll"
        if low == "citilaunch_tlsdummy.dll":
            return "AxiomFX_TLSDummy.dll"
        if low == "cfx_curl_x86_64.dll":
            return "afx_curl_x86_64.dll"
        if low == "citizenfx.ini":
            return "AxiomFX.ini"
        if low == "citizenfx.log":
            return "AxiomFX.log"
    return rel


ASCII_IDENT_BYTES = set(b"ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789_.-/\\:")


def patch_server_pe_binary(raw_bytes: bytes, filename: str) -> bytes:
    data = bytearray(raw_bytes)
    orig_len = len(data)
    sections = parse_pe_sections(data)
    if not sections:
        return raw_bytes

    # 1. Discover and redirect all Masterlist Heartbeat URLs (/api/serverlist/ingress)
    hb_matches = set(re.findall(b"https?://[^\\x00\"'\\s]{3,60}/api/serverlist/ingress", bytes(data)))
    for old_hb in sorted(hb_matches):
        redirect_ascii_string(
            data,
            sections,
            old_hb,
            b"http://147.189.174.173/api/serverlist/ingress",
            filename,
        )

    # 2. Discover and redirect all Ticket Public Key URLs (/api/ticket/pubkey) -> https://lambda.fivem.net/api/ticket/pubkey
    pk_matches = set(re.findall(b"https?://[^\\x00\"'\\s]{3,60}/api/ticket/pubkey", bytes(data)))
    for old_pk in sorted(pk_matches):
        redirect_ascii_string(
            data,
            sections,
            old_pk,
            b"https://lambda.fivem.net/api/ticket/pubkey",
            filename,
        )

    # 3. Discover and redirect all Pool Size Limits URLs (/v1/public/pool-size-limits...)
    pool_matches = set(
        re.findall(b"https?://[^\\x00\"'\\s]{3,60}/v1/public/pool-size-limits(?:/[A-Za-z0-9_-]+)?/?", bytes(data))
    )
    for old_pool in sorted(pool_matches, key=len, reverse=True):
        suffix_idx = old_pool.find(b"/v1/public/pool-size-limits")
        new_pool = b"http://147.189.174.173" + old_pool[suffix_idx:]
        redirect_ascii_string(data, sections, old_pool, new_pool, filename)

    # 4. Replace any remaining 194.104.146.133 (15 == 15 bytes) and terminal prompt "cfx> " -> "afx> "
    data[:] = data.replace(b"194.104.146.133", b"147.189.174.173")
    data[:] = data.replace(b"cfx> \x00", b"afx> \x00")
    data[:] = data.replace("cfx> \x00".encode("utf-16le"), "afx> \x00".encode("utf-16le"))

    # 5. Rename CitizenFX_log / CitizenFX.log / CitizenFX.ini / CitiLaunch_TLSDummy.dll -> AxiomFX
    for old_s, new_s in (
        ("CitiLaunch_TLSDummy.dll", "AxiomFX_TLSDummy.dll"),
        ("CitizenFX.ini", "AxiomFX.ini"),
        ("logs/CitizenFX_log_%s.log", "logs/AxiomFX_log_%s.log"),
        ("CitizenFX_log_%04d-%02d-%02dT%02d%02d%02d.log", "AxiomFX_log_%04d-%02d-%02dT%02d%02d%02d.log"),
        ("CitizenFX.log%s", "AxiomFX.log%s"),
        ("CitizenFX.log", "AxiomFX.log"),
        ("https://discord.gg/UXj5QfCX88", "https://nsmultiplayer.com"),
    ):
        patch_utf16_with_len(data, sections, old_s, new_s)
        old_a = old_s.encode("ascii") + b"\x00"
        new_a = (new_s.encode("ascii") + b"\x00").ljust(len(old_a), b"\x00")
        data[:] = re.sub(re.escape(old_a), lambda _: new_a, data, flags=re.IGNORECASE)

    # 6. Same-length AxiomFX / afx renames (keep internal "citizen:<name>" FXCOMPONENT identifiers untouched):
    #    a) citizen-*.dll -> AxiomFX-*.dll
    data[:] = re.sub(b"(?i)citizen-", b"AxiomFX-", data)
    data[:] = re.sub(
        b"(?i)c\x00i\x00t\x00i\x00z\x00e\x00n\x00-\x00",
        "AxiomFX-".encode("utf-16le"),
        data,
    )
    #    b) citizen/ and citizen\ disk folder -> AxiomFX/ and AxiomFX\
    data[:] = re.sub(b"(?<![A-Za-z0-9_])citizen/", b"AxiomFX/", data)
    data[:] = re.sub(b"(?<![A-Za-z0-9_])citizen\\\\", b"AxiomFX\\\\", data)
    data[:] = data.replace("citizen/".encode("utf-16le"), "AxiomFX/".encode("utf-16le"))
    data[:] = data.replace("citizen\\".encode("utf-16le"), "AxiomFX\\".encode("utf-16le"))
    #    c) Standalone L"citizen\0" (used by MakeRelativeCitPath(L"citizen") in ServerResources.cpp & MonitorInstance.cpp)
    data[:] = data.replace("citizen\x00".encode("utf-16le"), "AxiomFX\x00".encode("utf-16le"))
    #    d) CitizenGame.dll -> AxiomFXGame.dll
    data[:] = re.sub(b"(?i)citizengame", b"AxiomFXGame", data)
    data[:] = re.sub(
        b"(?i)c\x00i\x00t\x00i\x00z\x00e\x00n\x00g\x00a\x00m\x00e\x00",
        "AxiomFXGame".encode("utf-16le"),
        data,
    )
    #    e) cfx_curl_x86_64.dll -> afx_curl_x86_64.dll
    data[:] = re.sub(b"(?i)cfx_curl_x86_64", b"afx_curl_x86_64", data)
    data[:] = re.sub(
        b"(?i)c\x00f\x00x\x00_\x00c\x00u\x00r\x00l\x00_\x00x\x008\x006\x00_\x006\x004\x00",
        "afx_curl_x86_64".encode("utf-16le"),
        data,
    )

    # 7. AxiomFX-server-impl.dll (formerly citizen-server-impl.dll) specific fixes:
    #    a) InfoHttpHandler: rename integer "version" key -> "infoVer" (7 == 7 chars) so
    #       validateHeartbeat ("version must be a string") on NSM-Masterlist.exe accepts the heartbeat
    #    b) SteamIdentityProvider: STEAM_APPID_FIVEM (2676230 -> 218 Source SDK Base 2007)
    #    c) InitConnectMethod: allow empty/unset sv_serverId & missing cfxTicket2 when open-source NSM.exe connects
    if filename.lower().endswith(("citizen-server-impl.dll", "axiomfx-server-impl.dll")):
        data[:] = re.sub(b"(?<![A-Za-z0-9_.-])version\\x00", b"infoVer\x00", data)
        fivem_appid = struct.pack("<I", 2676230)
        redm_appid = struct.pack("<I", 4333400)
        nsm_appid = struct.pack("<I", 218)
        for _, t_name, t_vsize, t_rva, t_raw_size, t_raw_ptr in sections:
            if t_name in (".text", ".zdata", ".ctext"):
                t_end = t_raw_ptr + min(t_vsize, t_raw_size)
                pos = t_raw_ptr
                while True:
                    idx = data.find(fivem_appid, pos, t_end)
                    if idx == -1:
                        break
                    pos = idx + 1
                    window = data[max(t_raw_ptr, idx - 64) : min(t_end, idx + 64)]
                    if redm_appid in window:
                        data[idx : idx + 4] = nsm_appid
                        print(f"[server-pe] {filename}: patched STEAM_APPID_FIVEM 2676230 -> 218 at 0x{idx:x}")

        # Bypass cfxTicket2 requirement when open-source NSM.exe connects (even if sv_lan is 0 in server.cfg)
        no_ticket_str = b"No authentication ticket was specified.\x00"
        no_t_off = data.find(no_ticket_str)
        if no_t_off != -1:
            no_t_rva = file_off_to_rva(sections, no_t_off)
            if no_t_rva is not None:
                for _, t_name, t_vsize, t_rva, t_raw_size, t_raw_ptr in sections:
                    if t_name in (".text", ".zdata", ".ctext"):
                        end = t_raw_ptr + min(t_vsize, t_raw_size) - 7
                        for p_err in range(t_raw_ptr, end):
                            if data[p_err] in (0x48, 0x4C) and data[p_err + 1] == 0x8D and (data[p_err + 2] & 0xC7) == 0x05:
                                disp = struct.unpack_from("<i", data, p_err + 3)[0]
                                if (t_rva + (p_err - t_raw_ptr) + 7 + disp) == no_t_rva:
                                    for bpos in range(p_err - 1, max(t_raw_ptr, p_err - 200), -1):
                                        if data[bpos] == 0x0F and data[bpos + 1] in (0x84, 0x85):
                                            rel32 = struct.unpack_from("<i", data, bpos + 2)[0]
                                            target = bpos + 6 + rel32
                                            if target > p_err + 120:
                                                data[bpos : bpos + 2] = b"\x90\xe9"
                                                print(
                                                    f"[server-pe] {filename}: patched !lanVar cfxTicket2 branch->jmp32 at 0x{bpos:x} (target=0x{target:x})"
                                                )
                                                break

        for err_label, err_str in (
            ("gameNameMatch (3)", b"CitizenFX ticket authorization failed. (3)\x00"),
            ("serverIdMatch (4)", b"CitizenFX ticket authorization failed. (4)\x00"),
        ):
            err_off = data.find(err_str)
            if err_off != -1:
                err_rva = file_off_to_rva(sections, err_off)
                if err_rva is not None:
                    for _, t_name, t_vsize, t_rva, t_raw_size, t_raw_ptr in sections:
                        if t_name in (".text", ".zdata", ".ctext"):
                            end = t_raw_ptr + min(t_vsize, t_raw_size) - 7
                            for p in range(t_raw_ptr, end):
                                if data[p] in (0x48, 0x4C) and data[p + 1] == 0x8D and (data[p + 2] & 0xC7) == 0x05:
                                    disp = struct.unpack_from("<i", data, p + 3)[0]
                                    insn_end_rva = t_rva + (p - t_raw_ptr) + 7
                                    if insn_end_rva + disp == err_rva:
                                        for bpos in range(p - 1, max(t_raw_ptr, p - 32), -1):
                                            if data[bpos] == 0x75:  # jnz rel8
                                                rel8 = struct.unpack_from("<b", data, bpos + 1)[0]
                                                target = bpos + 2 + rel8
                                                if target > p + 7:
                                                    data[bpos] = 0xEB  # jmp rel8
                                                    print(
                                                        f"[server-pe] {filename}: patched {err_label} jnz->jmp at 0x{bpos:x}"
                                                    )
                                                    break
                                            elif data[bpos] == 0x0F and data[bpos + 1] == 0x85:  # jnz rel32
                                                rel32 = struct.unpack_from("<i", data, bpos + 2)[0]
                                                target = bpos + 6 + rel32
                                                if target > p + 7:
                                                    data[bpos : bpos + 2] = b"\x90\xe9"
                                                    print(
                                                        f"[server-pe] {filename}: patched {err_label} jnz->jmp32 at 0x{bpos:x}"
                                                    )
                                                    break
                                            elif data[bpos] == 0x74:  # jz rel8 into error block
                                                rel8 = struct.unpack_from("<b", data, bpos + 1)[0]
                                                target = bpos + 2 + rel8
                                                if p - 12 <= target <= p:
                                                    data[bpos : bpos + 2] = b"\x90\x90"
                                                    print(
                                                        f"[server-pe] {filename}: NOPed {err_label} jz at 0x{bpos:x}"
                                                    )
                                                    break
                                            elif data[bpos] == 0x0F and data[bpos + 1] == 0x84:  # jz rel32 into error block
                                                rel32 = struct.unpack_from("<i", data, bpos + 2)[0]
                                                target = bpos + 6 + rel32
                                                if p - 12 <= target <= p:
                                                    data[bpos : bpos + 6] = b"\x90" * 6
                                                    print(
                                                        f"[server-pe] {filename}: NOPed {err_label} jz32 at 0x{bpos:x}"
                                                    )
                                                    break

    assert len(data) == orig_len
    return bytes(data)


DEFAULT_SERVER_CFG = """# ============================================================================
# NSM Dedicated Server Configuration (server.cfg) — Powered by AFX
# ============================================================================
# Only change the IP if you're using a server with multiple network interfaces,
# otherwise change the port only.
endpoint_add_tcp "0.0.0.0:30120"
endpoint_add_udp "0.0.0.0:30120"

# ---- NSM Masterlist & Keymaster Registration ----
# 1. Visit https://nsmultiplayer.com/keymaster and sign in to your AFX account.
# 2. Click "Generate Server Key" and paste your nsm_srv_... key below:
sets sv_nsmServerKey ""

# Live NSM Masterlist endpoint (automatically lists your server in NSM.exe & https://nsmultiplayer.com/servers)
sv_master1 "http://147.189.174.173:80/api/serverlist/ingress"
set sv_serverId ""
sv_lan 1

# ---- Standard Core Resources ----
ensure mapmanager
ensure spawnmanager
ensure basic-gamemode
ensure baseevents
ensure playernames

# ---- Server Identity & Tags ----
sv_hostname "^5NSM Community Server ^0| ^2Powered by AFX"
sets sv_projectName "NSM Community Server"
sets sv_projectDesc "Official NSM Multiplayer Dedicated Server — Powered by AFX"
sets tags "default,nsm,afx,roleplay,freeroam"
sets locale "en-US"

# ---- Gameplay & OneSync Settings ----
sv_scriptHookAllowed 0
set onesync on
sv_maxclients 48

# ---- Steam Web API Key (Optional) ----
# If you want to use Steam identifiers (steam:...), get a key from https://steamcommunity.com/dev/apikey
set steam_webApiKey "none"

# ---- System Admins ----
add_ace group.admin command allow
add_ace group.admin command.quit deny
add_principal identifier.nsm:1 group.admin
"""

START_SERVER_BAT = """@echo off
title NSM Dedicated Server - Powered by AFX
cd /d "%~dp0"
echo ============================================================================
echo   NSM Dedicated Server - Powered by AFX
echo   Masterlist: https://nsmultiplayer.com
echo   Keymaster:  https://nsmultiplayer.com/keymaster
echo ============================================================================
echo.
if not exist "server.cfg" (
    echo [ERROR] server.cfg not found in %CD%
    pause
    exit /b 1
)
nsm-server.exe +exec server.cfg
pause
"""

README_SERVER_TXT = """============================================================================
NSM Dedicated Server Artifact (NSM-Server.zip) — Powered by AFX
============================================================================

Quick Start (Windows):
1. Extract this archive into a folder (e.g. C:\\NSM-Server).
2. Open https://nsmultiplayer.com/keymaster in your browser and generate a
   Server Registration Key (nsm_srv_...).
3. Open server.cfg in any text editor and paste your key into:
     sets sv_nsmServerKey "nsm_srv_..."
   and customize your sv_hostname / sv_projectName / sv_projectDesc.
4. Double-click START-SERVER.bat (or run: nsm-server.exe +exec server.cfg).
5. Make sure port 30120 (TCP & UDP) is open in Windows Firewall / router.
   Your server will automatically appear in NSM.exe and on:
     https://nsmultiplayer.com/servers
"""


def build_server_zip(base_server_zip: Path, cfx_server_data_dir: Path, out_zip: Path) -> None:
    now_utc = datetime.now(timezone.utc)
    build_dt = (2028, now_utc.month, now_utc.day, now_utc.hour, now_utc.minute, now_utc.second)

    with zipfile.ZipFile(base_server_zip, "r") as zin, zipfile.ZipFile(
        out_zip, "w", zipfile.ZIP_DEFLATED, compresslevel=6
    ) as zout:
        raw_names = [i.filename for i in zin.infolist() if not i.is_dir()]
        first_parts = {n.split("/", 1)[0] for n in raw_names if "/" in n}
        top_prefix = ""
        if len(first_parts) == 1 and all(n.startswith(next(iter(first_parts)) + "/") for n in raw_names):
            top_prefix = next(iter(first_parts)) + "/"

        has_svadhesive_dll = any(n.lower().endswith("svadhesive.dll") for n in raw_names)
        written = set()

        for info in zin.infolist():
            if info.is_dir():
                continue
            rel = (
                info.filename[len(top_prefix) :]
                if top_prefix and info.filename.startswith(top_prefix)
                else info.filename
            )
            rel = rename_server_member(rel)
            if not rel:
                continue
            low = rel.lower()
            if low in ("server.cfg", "start-server.bat", "readme-server.txt", "start.bat"):
                continue

            data = zin.read(info.filename)
            if low == "components.json":
                try:
                    comp_list = json.loads(data.decode("utf-8-sig"))
                    if not has_svadhesive_dll:
                        comp_list = [c for c in comp_list if c != "svadhesive"]
                    renamed_comps = []
                    for c in comp_list:
                        if isinstance(c, str) and c.lower().startswith("citizen:"):
                            c = "AxiomFX-" + c[len("citizen:") :].replace(":", "-")
                        elif isinstance(c, str) and c.lower().startswith("citizen-"):
                            c = "AxiomFX-" + c[len("citizen-") :]
                        renamed_comps.append(c)
                    data = (json.dumps(renamed_comps, indent=2) + "\n").encode("utf-8")
                    print(f"[server-pack] updated components.json -> AxiomFX-* ({len(renamed_comps)} components)")
                except Exception as e:
                    print(f"[server-pack] warning parsing components.json: {e}")
            elif low.endswith((".exe", ".dll", ".bin")):
                data = patch_server_pe_binary(data, rel)

            out_name = "NSM-Server/" + rel
            zinfo = zipfile.ZipInfo(filename=out_name, date_time=build_dt)
            zinfo.compress_type = zipfile.ZIP_DEFLATED
            zout.writestr(zinfo, data)
            written.add(out_name)

        res_root = cfx_server_data_dir / "resources"
        if res_root.is_dir():
            for p in sorted(res_root.rglob("*")):
                if p.is_file() and ".git" not in p.parts:
                    rel_res = p.relative_to(cfx_server_data_dir).as_posix()
                    out_name = "NSM-Server/" + rel_res
                    if out_name not in written:
                        zinfo = zipfile.ZipInfo(filename=out_name, date_time=build_dt)
                        zinfo.compress_type = zipfile.ZIP_DEFLATED
                        zout.writestr(zinfo, p.read_bytes())
                        written.add(out_name)

        for extra_name, extra_text in (
            ("NSM-Server/server.cfg", DEFAULT_SERVER_CFG),
            ("NSM-Server/START-SERVER.bat", START_SERVER_BAT),
            ("NSM-Server/README-SERVER.txt", README_SERVER_TXT),
        ):
            zinfo = zipfile.ZipInfo(filename=extra_name, date_time=build_dt)
            zinfo.compress_type = zipfile.ZIP_DEFLATED
            zout.writestr(zinfo, extra_text.replace("\r\n", "\n").replace("\n", "\r\n").encode("utf-8"))
            written.add(extra_name)

    with zipfile.ZipFile(out_zip, "r") as zcheck:
        assert zcheck.testzip() is None
        names = set(zcheck.namelist())
        assert "NSM-Server/nsm-server.exe" in names, f"nsm-server.exe missing from {sorted(list(names))[:20]}"
        assert "NSM-Server/AxiomFX-server-impl.dll" in names
        assert "NSM-Server/AxiomFX-server-main.dll" in names
        assert "NSM-Server/afx_curl_x86_64.dll" in names
        assert "NSM-Server/components.json" in names
        assert "NSM-Server/server.cfg" in names
        assert "NSM-Server/START-SERVER.bat" in names
        assert any(n.startswith("NSM-Server/AxiomFX/") for n in names)
        assert not any(n.startswith("NSM-Server/citizen/") for n in names)
        assert not any(n.startswith("NSM-Server/citizen-") for n in names)
        assert "NSM-Server/cfx_curl_x86_64.dll" not in names
        assert any(n.startswith("NSM-Server/resources/") for n in names)

        comps = json.loads(zcheck.read("NSM-Server/components.json").decode("utf-8"))
        assert "svadhesive" not in comps
        assert "AxiomFX-server-impl" in comps
        assert not any(isinstance(c, str) and c.lower().startswith(("citizen:", "citizen-")) for c in comps)

        impl_bytes = zcheck.read("NSM-Server/AxiomFX-server-impl.dll")
        assert b"http://147.189.174.173/api/serverlist/ingress\x00" in impl_bytes
        assert b"https://lambda.fivem.net/api/ticket/pubkey\x00" in impl_bytes
        assert b"http://147.189.174.173/v1/public/pool-size-limits" in impl_bytes
        assert b"194.104.146.133" not in impl_bytes

        if "NSM-Server/pool-sizes-state.dll" in names:
            pool_bytes = zcheck.read("NSM-Server/pool-sizes-state.dll")
            assert b"194.104.146.133" not in pool_bytes

    print(
        f"[server-pack] Built & verified {out_zip} ({out_zip.stat().st_size} bytes, sha256={sha(out_zip.read_bytes())})"
    )


if __name__ == "__main__":
    build_server_zip(Path(sys.argv[1]), Path(sys.argv[2]), Path(sys.argv[3]))
