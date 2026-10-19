"""Install the unchanged exact-port filter before enabling packet reception."""
import ctypes
import socket

def open_rtp_header_tap(port):
    assert 0 < port < 65536
    class Filter(ctypes.Structure):
        _fields_ = [('code', ctypes.c_ushort), ('jt', ctypes.c_ubyte), ('jf', ctypes.c_ubyte), ('k', ctypes.c_uint)]
    class Program(ctypes.Structure):
        _fields_ = [('length', ctypes.c_ushort), ('filters', ctypes.POINTER(Filter))]
    instructions = [(0x30,0,0,9),(0x15,0,7,17),(0xb1,0,0,0),(0x48,0,0,2),(0x15,0,4,port),(0x50,0,0,8),(0x54,0,0,0xc0),(0x15,0,1,0x80),(0x06,0,0,64),(0x06,0,0,0)]
    filters = (Filter * len(instructions))(*(Filter(*item) for item in instructions))
    program = Program(len(instructions), filters)
    # packet(7): protocol zero receives nothing until the nonzero bind.
    tap = socket.socket(socket.AF_PACKET, socket.SOCK_DGRAM, 0)
    try:
        tap.setsockopt(socket.SOL_SOCKET, 26, bytes(program))
        tap.bind(('lo', 0x0800))
        tap.settimeout(1)
        return tap
    except BaseException:
        tap.close()
        raise
