from src.calc import clamp

def test_in(): assert clamp(5, 0, 10) == 5
def test_lo_edge(): assert clamp(0, 0, 10) == 0
def test_hi_edge(): assert clamp(10, 0, 10) == 10
def test_out(): assert clamp(-3, 0, 10) == 0
