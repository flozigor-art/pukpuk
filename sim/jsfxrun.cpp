// jsfxrun <file.jsfx> <srate> <slider1> <in.f64> <out.f64>
// in/out: interleaved stereo float64.
#include "ysfx.h"
#include <cstdio>
#include <cstdlib>
#include <vector>
static void logr(intptr_t, ysfx_log_level l, const char *m){ fprintf(stderr,"[ysfx %d] %s\n",(int)l,m); }
int main(int argc,char**argv){
  if(argc<6){fprintf(stderr,"usage\n");return 2;}
  double sr=atof(argv[2]), s1=atof(argv[3]);
  FILE*f=fopen(argv[4],"rb"); fseek(f,0,SEEK_END); long n=ftell(f)/16; fseek(f,0,SEEK_SET);
  std::vector<double> in(2*n), out(2*n); if(fread(in.data(),8,2*n,f)!=(size_t)(2*n)){fprintf(stderr,"short read\n");return 1;} fclose(f);
  ysfx_config_t*cfg=ysfx_config_new(); ysfx_set_log_reporter(cfg,logr);
  ysfx_t*fx=ysfx_new(cfg);
  if(!ysfx_load_file(fx,argv[1],0)){fprintf(stderr,"load failed\n");return 1;}
  if(!ysfx_compile(fx,0)){fprintf(stderr,"compile failed\n");return 1;}
  const uint32_t bs=512; ysfx_set_block_size(fx,bs); ysfx_set_sample_rate(fx,sr);
  ysfx_init(fx); ysfx_slider_set_value(fx,0,s1);
  std::vector<double> L(bs),R(bs),OL(bs),OR(bs);
  for(long p=0;p<n;p+=bs){
    uint32_t m=(uint32_t)std::min<long>(bs,n-p);
    for(uint32_t i=0;i<m;i++){L[i]=in[2*(p+i)];R[i]=in[2*(p+i)+1];}
    const double*ins[2]={L.data(),R.data()}; double*outs[2]={OL.data(),OR.data()};
    ysfx_process_double(fx,ins,outs,2,2,m);
    for(uint32_t i=0;i<m;i++){out[2*(p+i)]=OL[i];out[2*(p+i)+1]=OR[i];}
  }
  f=fopen(argv[5],"wb"); fwrite(out.data(),8,2*n,f); fclose(f);
  ysfx_free(fx); ysfx_config_free(cfg); return 0;
}
