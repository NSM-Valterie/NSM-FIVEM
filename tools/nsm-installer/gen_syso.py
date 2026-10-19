#!/usr/bin/env python3
"""Generate x86_64 Windows COFF resource object (rsrc_windows_amd64.syso)
embedding the blue NSM icon (code/client/launcher/fivem.ico) and an asInvoker manifest.
"""
import struct
from pathlib import Path


def build_coff_syso(ico_path: Path, out_syso: Path) -> None:
    ico = ico_path.read_bytes()
    reserved, ico_type, count = struct.unpack_from("<HHH", ico, 0)
    assert reserved == 0 and ico_type == 1

    grp = bytearray(struct.pack("<HHH", 0, 1, count))
    icons = []
    for i in range(count):
        w, h, colors, res, planes, bitcount, bytes_in_res, img_off = struct.unpack_from(
            "<BBBBHHII", ico, 6 + i * 16
        )
        icon_id = i + 1
        grp += struct.pack(
            "<BBBBHHIH", w, h, colors, res, planes, bitcount, bytes_in_res, icon_id
        )
        icons.append((icon_id, ico[img_off : img_off + bytes_in_res]))

    manifest = b"""<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<assembly xmlns="urn:schemas-microsoft-com:asm.v1" manifestVersion="1.0">
  <trustInfo xmlns="urn:schemas-microsoft-com:asm.v3">
    <security>
      <requestedPrivileges>
        <requestedExecutionLevel level="asInvoker" uiAccess="false"/>
      </requestedPrivileges>
    </security>
  </trustInfo>
  <application xmlns="urn:schemas-microsoft-com:asm.v3">
    <windowsSettings>
      <dpiAware xmlns="http://schemas.microsoft.com/SMI/2005/WindowsSettings">true/pm</dpiAware>
    </windowsSettings>
  </application>
</assembly>"""

    total_items = count + 2
    l1_size = 16 + 3 * 8
    l2_icon_size = 16 + count * 8
    l2_grp_size = 16 + 1 * 8
    l2_man_size = 16 + 1 * 8
    l3_size = total_items * (16 + 1 * 8)
    data_entries_off = l1_size + l2_icon_size + l2_grp_size + l2_man_size + l3_size
    blobs_off = (data_entries_off + total_items * 16 + 7) & ~7

    rsrc = bytearray()
    rsrc += struct.pack("<IIHHHH", 0, 0, 0, 0, 0, 3)
    off_l2_icon = l1_size
    off_l2_grp = off_l2_icon + l2_icon_size
    off_l2_man = off_l2_grp + l2_grp_size
    rsrc += struct.pack("<II", 3, 0x80000000 | off_l2_icon)
    rsrc += struct.pack("<II", 14, 0x80000000 | off_l2_grp)
    rsrc += struct.pack("<II", 24, 0x80000000 | off_l2_man)

    off_l3_base = off_l2_man + l2_man_size
    rsrc += struct.pack("<IIHHHH", 0, 0, 0, 0, 0, count)
    for i in range(count):
        rsrc += struct.pack("<II", i + 1, 0x80000000 | (off_l3_base + i * 24))
    rsrc += struct.pack("<IIHHHH", 0, 0, 0, 0, 0, 1)
    rsrc += struct.pack("<II", 1, 0x80000000 | (off_l3_base + count * 24))
    rsrc += struct.pack("<IIHHHH", 0, 0, 0, 0, 0, 1)
    rsrc += struct.pack("<II", 1, 0x80000000 | (off_l3_base + (count + 1) * 24))

    for i in range(total_items):
        rsrc += struct.pack("<IIHHHH", 0, 0, 0, 0, 0, 1)
        rsrc += struct.pack("<II", 0x0409, data_entries_off + i * 16)

    assert len(rsrc) == data_entries_off

    all_blobs = [b for _, b in icons] + [bytes(grp), manifest]
    blob_zone = bytearray()
    relocs = []
    cur_blob_off = blobs_off
    for i, b in enumerate(all_blobs):
        rva_field_off = data_entries_off + i * 16
        rsrc += struct.pack("<IIII", cur_blob_off, len(b), 0, 0)
        relocs.append(rva_field_off)
        padded = b + b"\x00" * ((8 - (len(b) % 8)) % 8)
        blob_zone += padded
        cur_blob_off += len(padded)

    rsrc += b"\x00" * (blobs_off - len(rsrc))
    rsrc += blob_zone

    raw_data_ptr = 20 + 40
    reloc_ptr = raw_data_ptr + len(rsrc)
    sym_ptr = reloc_ptr + len(relocs) * 10

    coff = bytearray()
    coff += struct.pack("<HHIIIHH", 0x8664, 1, 0, sym_ptr, 1, 0, 0)
    coff += struct.pack(
        "<8sIIIIIIHHI",
        b".rsrc\x00\x00\x00",
        0,
        0,
        len(rsrc),
        raw_data_ptr,
        reloc_ptr,
        0,
        len(relocs),
        0,
        0x40300040,
    )
    coff += rsrc
    for r_off in relocs:
        coff += struct.pack("<IIH", r_off, 0, 3)
    coff += struct.pack("<8sIhHBB", b".rsrc\x00\x00\x00", 0, 1, 0, 3, 0)
    coff += struct.pack("<I", 4)
    out_syso.write_bytes(coff)


if __name__ == "__main__":
    repo = Path(__file__).resolve().parents[2]
    build_coff_syso(
        repo / "code" / "client" / "launcher" / "fivem.ico",
        Path(__file__).resolve().parent / "rsrc_windows_amd64.syso",
    )
