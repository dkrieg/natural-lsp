* External subroutine with DEFINE DATA PARAMETER
* This should be shadowed by the inline MY-SUB in PERFCALLER.NSP (FR-12: inline-before-external)
* Feature 17, T4a, Story 2 AC1/AC2
DEFINE DATA
PARAMETER
  1 PARAM-A (N8)
  1 PARAM-B (A20)
END DEFINE

WRITE 'External subroutine MY-SUB'

END
