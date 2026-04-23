function ci   = sigprocae(rawsig,t)%#codegen
%this function generates CI vector from AE signal collected. 
%rawsig is the time series signal, fs is the sample rate(Hz). 
%first, heterodyning the signal between fband(1) and fband(2); 
%then calculate CI's in the following order: 
%[mean_t, rms_t, peak_t, skew_t kurtosis_t]
%also provides the raw (heterodyned signal) raw_t; 
%%%%%%%%%%%%%%%%%%%%%%%%%%%%%%%%%%%%%%%%%%%%%%%%%%%%%%%%%%%%%%%%%%%
%declare variables for size%
persistent firsttime collectedData
% arguments 
%    rawsig (:,1) double
%    startTime (1,1) double
%    collectedData (:,1) double
% end 
if isempty(firsttime)
        firsttime = 1; % Start timer on first call
        collectedData = [rawsig;]; % Initialize data buffer
end
    if mod (t,10)==0 
        % Collect data for 10 seconds
        collectedData = [collectedData; rawsig;]; % Append new data
        ci = zeros(5,1); % Or some placeholder value
        % Process data after 10 seconds
        % start processing%
        ci(1,1) = mean(collectedData);
        ci(2,1) = rms(collectedData);
        ci(3,1) = peak2peak(collectedData);
        ci(4,1) = skewness(collectedData);
        ci(5,1) = kurtosis(collectedData); 
        collectedData = [0]; % Reset for next 10-second cycle 
    else 
        ci = zeros(5,1);
    end

end 
