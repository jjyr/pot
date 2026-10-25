"""Read embedded Go string globals from released ELF/Mach-O, without execution.

Go omits -ldflags from build info when -trimpath is set. Stripped releases also
lack symbol tables. Require exact string headers in writable load segments,
pointing to the expected bytes in file-backed load segments, rather than treating
an arbitrary substring in executable bytes as a version declaration. VCS/target
identity is separately read with the official Go build-info reader.
"""

import struct


def embedded_go_strings(data, values):
    """Check linked string values in current 64-bit little-endian release formats."""
    segments = []
    if data[:6] == b"\x7fELF\x02\x01":
        phoff = struct.unpack_from("<Q", data, 32)[0]
        size, count = struct.unpack_from("<HH", data, 54)
        for index in range(count):
            offset = phoff + index * size
            kind, flags, fileoff, address, _, filesz = struct.unpack_from("<IIQQQQ", data, offset)
            if kind == 1:  # PT_LOAD
                segments.append((address, fileoff, filesz, bool(flags & 2)))
    elif data[:4] == b"\xcf\xfa\xed\xfe":
        count = struct.unpack_from("<I", data, 16)[0]
        offset = 32
        for _ in range(count):
            kind, size = struct.unpack_from("<II", data, offset)
            if size < 8:
                raise ValueError("invalid Mach-O load command")
            if kind == 0x19:  # LC_SEGMENT_64
                address, _, fileoff, filesz, _, protection = struct.unpack_from(
                    "<QQQQII", data, offset + 24
                )
                segments.append((address, fileoff, filesz, bool(protection & 2)))
            offset += size
    else:
        raise ValueError("unsupported release executable format")
    if not segments or any(fileoff + size > len(data) for _, fileoff, size, _ in segments):
        raise ValueError("invalid executable load segments")
    writable = [
        (fileoff, data[fileoff : fileoff + size]) for _, fileoff, size, write in segments if write
    ]
    for label, value in values.items():
        expected = value.encode()
        found = False
        for address, fileoff, size, _ in segments:
            cursor = fileoff
            while (position := data.find(expected, cursor, fileoff + size)) >= 0:
                pointer = address + position - fileoff
                header = struct.pack("<QQ", pointer, len(expected))
                for base, region in writable:
                    search = 0
                    while (location := region.find(header, search)) >= 0:
                        if (base + location) % 8 == 0:
                            found = True
                            break
                        search = location + 1
                    if found:
                        break
                if found:
                    break
                cursor = position + 1
            if found:
                break
        if not found:
            raise ValueError(f"embedded Go string global mismatch: {label}")
    return list(values)
