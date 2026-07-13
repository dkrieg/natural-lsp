* External subroutine with parameters (≥2, including an array)
* Performed by PERFEXT.NSP
* Feature 17, T4a, Story 2 AC1/AC2/AC3
DEFINE DATA
PARAMETER
  1 EXT-NUM (N8)
  1 EXT-NAME (A30)
  1 EXT-VALS (P7.2 / 1:10)
END DEFINE

WRITE 'External subroutine EXTSUB'

END
