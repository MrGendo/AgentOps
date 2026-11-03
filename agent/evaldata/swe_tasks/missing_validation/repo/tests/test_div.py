import pytest
from src.div import safe_div

def test_z():
    with pytest.raises(ValueError, match='denominator'): safe_div(1,0)
